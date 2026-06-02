package repo

import "testing"

func TestNewPoolConfigAppliesDatabaseURLAndMaxConns(t *testing.T) {
	cfg, err := NewPoolConfig("postgres://user:pass@localhost:5432/app?sslmode=disable", 7)
	if err != nil {
		t.Fatalf("new pool config: %v", err)
	}

	if cfg.ConnConfig.Host != "localhost" {
		t.Fatalf("host = %q, want localhost", cfg.ConnConfig.Host)
	}
	if cfg.MaxConns != 7 {
		t.Fatalf("MaxConns = %d, want 7", cfg.MaxConns)
	}
}

func TestNewPoolConfigRejectsInvalidURL(t *testing.T) {
	_, err := NewPoolConfig("not a postgres url", 10)
	if err == nil {
		t.Fatal("expected invalid database URL to fail")
	}
}
