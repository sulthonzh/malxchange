// Package service is the shared boot harness every service binary runs:
// load config → connect Postgres (own role/schema) → run embedded
// migrations → connect Redpanda → create owned topics → start the outbox
// relay → serve /healthz + /readyz → run service-specific extras → wait
// for shutdown. Later slices plug their logic in via Extra.
package service

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/health"
	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/migrations"
	"github.com/syaikhipin/malxchange/internal/outbox"
	"github.com/syaikhipin/malxchange/internal/pg"
	"github.com/syaikhipin/malxchange/internal/pgmig"
)

// Deps are the live connections a service's Extra function builds on.
type Deps struct {
	Cfg      config.Config
	Pool     *pgxpool.Pool
	Producer *kgo.Client
	Outbox   *outbox.Outbox
	Log      *slog.Logger
	// Internal is mounted at /internal/ for service-to-service RPC and
	// admin views (engine status etc.). Compose keeps the port off Caddy.
	Internal *http.ServeMux
	// Public is mounted at the listener root ("/"): the member surface
	// Caddy proxies to (issue #24 — /api/* REST, /ws WebSocket). Health
	// (/healthz, /readyz) stays its fallback, so a service that mounts
	// nothing public keeps today's behavior.
	Public *http.ServeMux
}

// Extra is service-specific boot logic; nil is valid (skeleton services).
// It runs after all infrastructure is up and must return only on failure.
type Extra func(ctx context.Context, d Deps) error

// Main is the entire body of a service main(): it handles the -healthcheck
// flag used by the compose healthcheck, then runs the boot sequence.
func Main(service string, extra Extra) {
	hc := flag.Bool("healthcheck", false, "probe the local health endpoint and exit")
	flag.Parse()
	if *hc {
		os.Exit(healthcheckSelf())
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(ctx, service, extra, log); err != nil && ctx.Err() == nil {
		log.Error("service exited", "service", service, "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, service string, extra Extra, log *slog.Logger) error {
	cfg, err := config.Load(service)
	if err != nil {
		return err
	}
	log.Info("booting", "service", service, "addr", cfg.HTTPAddr)

	pool, err := pg.Connect(ctx, cfg.PG)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pgmig.Run(cfg.PG.URL(), migrations.FS, service); err != nil {
		return err
	}
	log.Info("migrations applied", "schema", cfg.PG.Schema)

	producer, err := kafka.NewProducer(cfg.Kafka)
	if err != nil {
		return err
	}
	defer producer.Close()

	// Boot continues if the broker is briefly unavailable (compose starts
	// services in parallel); readiness reports it, and topic creation
	// retries below. Producing is impossible until topics exist.
	if err := ensureTopicsRetry(ctx, producer, service, log); err != nil {
		return err
	}

	relay := outbox.NewRelay(pool, kafka.Publisher{Client: producer})
	go func() {
		if err := relay.Run(ctx); err != nil {
			log.Error("outbox relay stopped", "err", err)
		}
	}()

	root := http.NewServeMux()
	checks := map[string]health.Check{
		"postgres": pool.Ping,
		"redpanda": func(c context.Context) error { return kafka.Ping(c, producer) },
	}
	internalMux := http.NewServeMux()
	root.Handle("/internal/", http.StripPrefix("/internal", internalMux))
	// Public service surface (issue #24): the root mux hands everything
	// that is not /internal/ to the public mux, whose own "/" fallback is
	// still health — so member routes (api's /api/*, /ws) and the health
	// endpoints share the listener unchanged for the other services.
	publicMux := http.NewServeMux()
	publicMux.Handle("/", health.Handler(checks))
	root.Handle("/", publicMux)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.ListenAndServe() }()

	d := Deps{Cfg: cfg, Pool: pool, Producer: producer, Outbox: outbox.New(pool), Log: log, Internal: internalMux, Public: publicMux}
	if extra != nil {
		if err := extra(ctx, d); err != nil {
			return fmt.Errorf("extra: %w", err)
		}
	}

	select {
	case <-ctx.Done():
		log.Info("shutting down", "service", service)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-srvErr:
		return err
	}
}

// ensureTopicsRetry waits for broker reachability, then creates the
// service's owned topics; it gives up after 2 minutes of failure.
func ensureTopicsRetry(ctx context.Context, producer *kgo.Client, service string, log *slog.Logger) error {
	owned := events.OwnedTopics(service)
	if len(owned) == 0 {
		return nil
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := kafka.Ping(pingCtx, producer)
		cancel()
		if err == nil {
			defs := make([]kafka.TopicDef, 0, len(owned))
			for _, t := range owned {
				defs = append(defs, kafka.TopicDef{Name: t.Topic, Partitions: t.Partitions, Retention: t.Retention})
			}
			if err := kafka.EnsureTopics(ctx, producer, defs); err != nil {
				return err
			}
			log.Info("topics ensured", "count", len(defs))
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("kafka unreachable after retry window: %w", err)
		}
		log.Warn("waiting for broker", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// healthcheckSelf probes http://127.0.0.1<addr>/healthz — the compose
// healthcheck runs the same binary with -healthcheck (distroless images
// carry no curl/wget).
func healthcheckSelf() int {
	addr := os.Getenv("MAL_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 { // strip anything path-like
		addr = addr[:i]
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
