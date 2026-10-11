// Package kafka is the one place franz-go is configured (ADR-0002/0004):
// SASL SCRAM-SHA-256 against Redpanda, idempotent producer with acks=all,
// lz4 batching, record keys = business keys (per-entity ordering), and
// cooperative-sticky consumers with autocommit off — consumers commit
// offsets only after their Postgres transaction (ADR-0010 discipline).
package kafka

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/outbox"
)

func clientOpts(cfg config.Kafka) []kgo.Opt {
	return []kgo.Opt{
		kgo.SeedBrokers(cfg.Seeds...),
		kgo.SASL(scram.Sha256(func(context.Context) (scram.Auth, error) {
			return scram.Auth{User: cfg.User, Pass: cfg.Password}, nil
		})),
		kgo.ClientID("malxchange"),
		kgo.RequestTimeoutOverhead(5 * time.Second),
	}
}

// NewProducer returns a client tuned for publishing: idempotent by default
// in franz-go, all-ISR acks, lz4 compression, small linger for batching.
func NewProducer(cfg config.Kafka) (*kgo.Client, error) {
	opts := append(clientOpts(cfg),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.Lz4Compression()),
		kgo.ProducerLinger(20*time.Millisecond),
		kgo.MaxBufferedRecords(100_000),
	)
	c, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: producer: %w", err)
	}
	return c, nil
}

// NewConsumer returns a consumer-group client with autocommit disabled;
// the caller owns Poll/CommitMarkedOffset ordering (commit only after the
// domain transaction, per the engine's tx-then-offset rule).
func NewConsumer(cfg config.Kafka, group string, topics []string, extra ...kgo.Opt) (*kgo.Client, error) {
	opts := append(clientOpts(cfg),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	)
	c, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: consumer: %w", err)
	}
	return c, nil
}

// Ping verifies broker connectivity; used by readiness checks.
func Ping(ctx context.Context, c *kgo.Client) error {
	return c.Ping(ctx)
}

// TopicDef declares a topic this service produces. Partition count is 1
// for v1 (single engine replica, total per-topic ordering); retention is
// per-topic because market data (plate/ticker) is transient while fills
// are settlement history.
type TopicDef struct {
	Name       string
	Partitions int32
	Retention  time.Duration
}

// EnsureTopics idempotently creates defs (auto-create is off on the
// broker, so a typo cannot spawn a phantom topic). Creation is attempted
// unconditionally — ListTopics answers "unknown topic" with an errored
// entry that is indistinguishable from existence here, and TOPIC_ALREADY_
// EXISTS is the cheap idempotence signal. Existing topics are otherwise
// left untouched — partition/retention changes are deliberate operator
// actions, not boot behavior.
func EnsureTopics(ctx context.Context, c *kgo.Client, defs []TopicDef) error {
	admin := kadm.NewClient(c)
	for _, d := range defs {
		resp, err := admin.CreateTopic(ctx, d.Partitions, 1, map[string]*string{
			"retention.ms": new(fmt.Sprintf("%d", d.Retention.Milliseconds())),
		}, d.Name)
		if err != nil {
			// kadm reports "already exists" either way depending on shape.
			if strings.Contains(err.Error(), "TOPIC_ALREADY_EXISTS") {
				continue
			}
			return fmt.Errorf("kafka: create topic %s: %w", d.Name, err)
		}
		if resp.Err != nil && !strings.Contains(resp.Err.Error(), "TOPIC_ALREADY_EXISTS") {
			return fmt.Errorf("kafka: create topic %s: %w", d.Name, resp.Err)
		}
	}
	return nil
}

// Publisher adapts a producer client to the outbox relay: synchronous
// produce with the record key set (business key → partition ordering).
type Publisher struct{ Client *kgo.Client }

func (p Publisher) Publish(ctx context.Context, topic, key string, payload []byte) error {
	res := p.Client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: payload,
	})
	if err := res.FirstErr(); err != nil {
		return fmt.Errorf("kafka: produce %s: %w", topic, err)
	}
	return nil
}

var _ outbox.Publisher = Publisher{}
