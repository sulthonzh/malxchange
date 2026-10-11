package exchange

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/service"
)

// Run is the exchange service Extra (ADR-0010): recover the Books from
// Postgres, publish the initial Depth, then run the consumer, the TTL
// sweeper, and the config watcher until shutdown. A consumer failure is
// fatal on purpose: the process exits, recovery rebuilds the fold state,
// and Redpanda drains whatever buffered meanwhile.
func Run(ctx context.Context, d service.Deps) error {
	eng := NewEngine(d.Pool, d.Log, outbox.New(d.Pool))
	if err := eng.Recover(ctx); err != nil {
		return err
	}
	d.Log.Info("engine recovered", "markets", len(eng.Status()))

	consumer, err := kafka.NewConsumer(d.Cfg.Kafka, ConsumerGroup, []string{"order.command"})
	if err != nil {
		return err
	}
	defer consumer.Close()

	d.Internal.HandleFunc("GET /engine/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "markets": eng.Status(), "at": time.Now().UTC()})
	})

	errCh := make(chan error, 3)
	go func() { errCh <- ConsumeCommandTopic(ctx, eng, consumer) }()
	go func() { errCh <- SweepTTL(ctx, eng, d.Producer) }()
	go func() { errCh <- WatchConfig(ctx, eng) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
