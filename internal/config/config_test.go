package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "pg-secret")
	t.Setenv("MAL_KAFKA_USER", "kafka-user")
	t.Setenv("MAL_KAFKA_PASSWORD", "kafka-secret")
	for _, k := range []string{
		"MAL_HTTP_ADDR", "MAL_PG_HOST", "MAL_PG_PORT", "MAL_PG_DATABASE",
		"MAL_PG_USER", "MAL_PG_SCHEMA", "MAL_KAFKA_SEEDS",
	} {
		t.Setenv(k, "")
	}

	cfg, err := Load("api")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.PG.Host != "postgres" || cfg.PG.Port != "5432" || cfg.PG.Database != "malxchange" {
		t.Errorf("PG defaults wrong: %+v", cfg.PG)
	}
	if cfg.PG.User != "api" || cfg.PG.Schema != "api" {
		t.Errorf("PG user/schema should default to service name: %+v", cfg.PG)
	}
	if len(cfg.Kafka.Seeds) != 1 || cfg.Kafka.Seeds[0] != "redpanda:9092" {
		t.Errorf("Kafka seeds = %v", cfg.Kafka.Seeds)
	}
}

func TestLoadRejectsUnknownService(t *testing.T) {
	if _, err := Load("otc"); err == nil {
		t.Fatal("Load(otc) should fail: not a v1 service")
	}
}

func TestLoadRequiresSecrets(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "")
	t.Setenv("MAL_KAFKA_USER", "")
	t.Setenv("MAL_KAFKA_PASSWORD", "")
	if _, err := Load("wallet"); err == nil {
		t.Fatal("Load should fail without PG/Kafka credentials")
	}
}

func TestLoadRejectsUnsafeSchema(t *testing.T) {
	t.Setenv("MAL_PG_PASSWORD", "x")
	t.Setenv("MAL_KAFKA_USER", "x")
	t.Setenv("MAL_KAFKA_PASSWORD", "x")
	t.Setenv("MAL_PG_SCHEMA", `api"; DROP SCHEMA x`)
	if _, err := Load("api"); err == nil {
		t.Fatal("Load should reject a schema name that is not a plain identifier")
	}
}

func TestPGURLCarriesSearchPath(t *testing.T) {
	u := PG{Host: "db", Port: "5432", Database: "malxchange", User: "wallet", Password: "p/w", Schema: "wallet"}.URL()
	if want := "postgres://wallet:p%2Fw@db:5432/malxchange?search_path=wallet&sslmode=disable"; u != want {
		t.Errorf("URL = %q, want %q", u, want)
	}
}
