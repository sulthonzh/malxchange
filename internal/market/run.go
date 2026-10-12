package market

import (
	"context"

	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/service"
)

// Run is the market service Extra: the sole writer of the derived schema
// consumes trade.fill / market.plate / market.updated through the shared
// consumer group (per-Market order rides the Market symbol record key)
// behind the idempotent inbox, publishes the rolling 24h Ticker onto the
// ticker topic at the ≤1s cadence (ticket #32), and serves the internal
// market-data REST seams (ADR-0011 — market stays headless) — including
// the rebuild drill trigger, which rewinds the same consumer the loop
// polls (ticket #34). A consumer failure is fatal by design, same
// contract as the exchange engine: the process exits and Redpanda
// re-delivers from the committed offset.
func Run(ctx context.Context, d service.Deps) error {
	consumer, err := kafka.NewConsumer(d.Cfg.Kafka, ConsumerGroup, Topics)
	if err != nil {
		return err
	}
	defer consumer.Close()

	// The projector couples the loop and the rebuild: the endpoint can
	// only rewind the consumer this very loop polls (ticket #34).
	proj := NewProjector(d.Pool, d.Log, consumer)
	RegisterRPC(d.Internal, &RPC{DB: d.Pool, Rebuild: proj})

	errCh := make(chan error, 2)
	go func() { errCh <- proj.Consume(ctx) }()
	go func() { errCh <- PublishTickers(ctx, d.Pool, d.Log, d.Producer) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
