package config

import "testing"

func TestLoadWalletFromLookupRequiresDatabaseURL(t *testing.T) {
	_, err := LoadWalletFromLookup(mapLookup(nil))
	if err == nil {
		t.Fatal("expected missing DATABASE_URL to fail")
	}
}

func TestLoadWalletFromLookupDoesNotRequireJWTSecret(t *testing.T) {
	cfg, err := LoadWalletFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
	}))
	if err != nil {
		t.Fatalf("load wallet config: %v", err)
	}

	if cfg.GRPCAddr != ":9090" {
		t.Fatalf("GRPCAddr = %q, want :9090", cfg.GRPCAddr)
	}
	if cfg.DBMaxConns != 10 {
		t.Fatalf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
}

func TestLoadWalletFromLookupUsesCustomValues(t *testing.T) {
	cfg, err := LoadWalletFromLookup(mapLookup(map[string]string{
		"APP_ENV":      "test",
		"GRPC_ADDR":    ":19090",
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"DB_MAX_CONNS": "25",
	}))
	if err != nil {
		t.Fatalf("load wallet config: %v", err)
	}

	if cfg.AppEnv != "test" {
		t.Fatalf("AppEnv = %q, want test", cfg.AppEnv)
	}
	if cfg.GRPCAddr != ":19090" {
		t.Fatalf("GRPCAddr = %q, want :19090", cfg.GRPCAddr)
	}
	if cfg.DBMaxConns != 25 {
		t.Fatalf("DBMaxConns = %d, want 25", cfg.DBMaxConns)
	}
}

func TestLoadWalletFromLookupRejectsInvalidDBMaxConns(t *testing.T) {
	_, err := LoadWalletFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"DB_MAX_CONNS": "nope",
	}))
	if err == nil {
		t.Fatal("expected invalid DB_MAX_CONNS to fail")
	}
}

func TestLoadLedgerFromLookupUsesLedgerGRPCDefault(t *testing.T) {
	cfg, err := LoadLedgerFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/ledger?sslmode=disable",
	}))
	if err != nil {
		t.Fatalf("load ledger config: %v", err)
	}

	if cfg.GRPCAddr != ":9091" {
		t.Fatalf("GRPCAddr = %q, want :9091", cfg.GRPCAddr)
	}
	if cfg.DBMaxConns != 10 {
		t.Fatalf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
}
