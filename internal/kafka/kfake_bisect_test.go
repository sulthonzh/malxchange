package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

// kfakeBisect tries progressively richer consumer option sets to find the
// one that stops delivery against the in-process broker.
func kfakeBisect(t *testing.T, name string, extra ...kgo.Opt) {
	t.Helper()
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	addr := broker.ListenAddrs()[0]

	if err := broker.CreateTopic("probe.t", 1, nil); err != nil {
		t.Fatal(err)
	}
	p, err := kgo.NewClient(kgo.SeedBrokers(addr))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := p.ProduceSync(ctx, &kgo.Record{Topic: "probe.t", Value: []byte("x")}).FirstErr(); err != nil {
		t.Fatalf("%s: produce: %v", name, err)
	}

	opts := append([]kgo.Opt{
		kgo.SeedBrokers(addr),
		kgo.ConsumerGroup("g-" + name),
		kgo.ConsumeTopics("probe.t"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}, extra...)
	c, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	got := ""
	for rec := range c.PollRecords(ctx, 5).RecordsAll() {
		if rec.Topic == "probe.t" {
			got = string(rec.Value)
			break
		}
	}
	if got != "x" {
		t.Errorf("%s: no delivery", name)
	}
}

func TestKfakeMinimal(t *testing.T)      { kfakeBisect(t, "minimal") }
func TestKfakeNoAutocommit(t *testing.T) { kfakeBisect(t, "noac", kgo.DisableAutoCommit()) }
func TestKfakeSticky(t *testing.T)       { kfakeBisect(t, "sticky", kgo.Balancers(kgo.StickyBalancer())) }
func TestKfakeLingerLz4(t *testing.T) {
	kfakeBisect(t, "linger", kgo.ProducerLinger(20*time.Millisecond), kgo.ProducerBatchCompression(kgo.Lz4Compression()))
}

func TestKfakeClientID(t *testing.T) { kfakeBisect(t, "cid", kgo.ClientID("malxchange")) }
func TestKfakeTimeoutOverhead(t *testing.T) {
	kfakeBisect(t, "rto", kgo.RequestTimeoutOverhead(5*time.Second))
}

func TestKfakeAllISR(t *testing.T) {
	kfakeBisect(t, "allisr", kgo.RequiredAcks(kgo.AllISRAcks()))
}
