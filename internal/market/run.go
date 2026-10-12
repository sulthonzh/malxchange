package market

import (
	"context"

	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/service"
)

// Run is the market service Extra: the sole writer of the derived schema
// consumes trade.fill / market.plate through the shared consumer group
// (per-Market order rides the Market symbol record key) behind the
// idempotent inbox, and serves the internal market-data REST seams
// (ADR-0011 — market stays headless). A consumer failure is fatal by
// design, same contract as the exchange engine: the process exits and
// Redpanda re-delivers from the committed offset.
func Run(ctx context.Context, d service.Deps) error {
	consumer, err := kafka.NewConsumer(d.Cfg.Kafka, ConsumerGroup, []string{
		events.TopicTradeFill, events.TopicMarketPlate,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()

	RegisterRPC(d.Internal, &RPC{DB: d.Pool})

	errCh := make(chan error, 1)
	go func() { errCh <- ConsumeTopics(ctx, d.Pool, d.Log, consumer) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
