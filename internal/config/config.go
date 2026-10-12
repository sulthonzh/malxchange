// Package config loads service configuration from the environment.
//
// Every service binary shares the same MAL_* environment contract:
//
//	MAL_HTTP_ADDR            listen address for health/readyz (default ":8080")
//	MAL_PG_HOST              Postgres host (default "postgres")
//	MAL_PG_PORT              Postgres port (default "5432")
//	MAL_PG_DATABASE          database name (default "malxchange")
//	MAL_PG_USER              role name (default: the service name)
//	MAL_PG_PASSWORD          required
//	MAL_PG_SCHEMA            owning schema (default: the service name)
//	MAL_KAFKA_SEEDS          comma-separated brokers (default "redpanda:9092")
//	MAL_KAFKA_USER           required
//	MAL_KAFKA_PASSWORD       required
//
// The installer (install/install.sh) generates .env values for all of these.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Services is the fixed list of v1 services (context map, wayfinder #4).
var Services = []string{"api", "exchange", "market", "wallet", "admin"}

// Config is the resolved configuration for one service instance.
type Config struct {
	Service  string
	HTTPAddr string
	PG       PG
	Kafka    Kafka
}

// PG is the per-service Postgres connection: one role, one schema (ADR-0001).
type PG struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
	Schema   string
}

// URL returns the Postgres connection string: no TLS inside the compose
// network, and search_path pinned to the owning schema so that migrations
// and queries land in the service's schema without cross-service access.
func (p PG) URL() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.User, p.Password),
		Host:     p.Host + ":" + p.Port,
		Path:     "/" + p.Database,
		RawQuery: "sslmode=disable",
	}
	// search_path is passed as a startup parameter; schemas are [a-z_]+ so
	// QueryEscape is stable.
	q := u.Query()
	q.Set("search_path", p.Schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// Kafka is the Redpanda (Kafka API) client configuration (ADR-0002/0004).
type Kafka struct {
	Seeds    []string
	User     string
	Password string
}

// Load reads the environment for the named service and validates it.
func Load(service string) (Config, error) {
	if !isValidService(service) {
		return Config{}, fmt.Errorf("config: unknown service %q", service)
	}
	cfg := Config{
		Service:  service,
		HTTPAddr: envDefault("MAL_HTTP_ADDR", ":8080"),
		PG: PG{
			Host:     envDefault("MAL_PG_HOST", "postgres"),
			Port:     envDefault("MAL_PG_PORT", "5432"),
			Database: envDefault("MAL_PG_DATABASE", "malxchange"),
			User:     envDefault("MAL_PG_USER", service),
			Password: os.Getenv("MAL_PG_PASSWORD"),
			Schema:   envDefault("MAL_PG_SCHEMA", service),
		},
		Kafka: Kafka{
			Seeds:    splitSeeds(envDefault("MAL_KAFKA_SEEDS", "redpanda:9092")),
			User:     os.Getenv("MAL_KAFKA_USER"),
			Password: os.Getenv("MAL_KAFKA_PASSWORD"),
		},
	}
	if cfg.PG.Password == "" {
		return cfg, fmt.Errorf("config: MAL_PG_PASSWORD is required")
	}
	if cfg.Kafka.User == "" || cfg.Kafka.Password == "" {
		return cfg, fmt.Errorf("config: MAL_KAFKA_USER and MAL_KAFKA_PASSWORD are required")
	}
	// Schema is interpolated into DDL; keep it to a safe identifier shape.
	if !isIdentifier(cfg.PG.Schema) {
		return cfg, fmt.Errorf("config: MAL_PG_SCHEMA %q is not a valid schema name", cfg.PG.Schema)
	}
	if !isIdentifier(cfg.PG.User) {
		return cfg, fmt.Errorf("config: MAL_PG_USER %q is not a valid role name", cfg.PG.User)
	}
	return cfg, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvDefault returns the env var's value, or def when unset/empty.
// Exported so service packages with their own MAL_* sections reuse one
// helper instead of re-declaring it.
func EnvDefault(key, def string) string { return envDefault(key, def) }

func splitSeeds(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isValidService(s string) bool {
	for _, svc := range Services {
		if svc == s {
			return true
		}
	}
	return false
}

// isIdentifier accepts [a-z_][a-z0-9_]* — the shape of our schema/role names.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
