package config

import "testing"

func TestLoadFromLookupRequiresDatabaseURL(t *testing.T) {
	_, err := LoadFromLookup(mapLookup(map[string]string{
		"JWT_SECRET": "dev-secret",
	}))
	if err == nil {
		t.Fatal("expected missing DATABASE_URL to fail")
	}
}

func TestLoadFromLookupRequiresJWTSecret(t *testing.T) {
	_, err := LoadFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
	}))
	if err == nil {
		t.Fatal("expected missing JWT_SECRET to fail")
	}
}

func TestLoadFromLookupUsesDefaults(t *testing.T) {
	cfg, err := LoadFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"JWT_SECRET":   "dev-secret",
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.AppEnv != "local" {
		t.Fatalf("AppEnv = %q, want local", cfg.AppEnv)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.DBMaxConns != 10 {
		t.Fatalf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
}

func TestLoadFromLookupUsesCustomValues(t *testing.T) {
	cfg, err := LoadFromLookup(mapLookup(map[string]string{
		"APP_ENV":      "test",
		"HTTP_ADDR":    ":9090",
		"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"JWT_SECRET":   "dev-secret",
		"DB_MAX_CONNS": "25",
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.AppEnv != "test" {
		t.Fatalf("AppEnv = %q, want test", cfg.AppEnv)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.DBMaxConns != 25 {
		t.Fatalf("DBMaxConns = %d, want 25", cfg.DBMaxConns)
	}
}

func TestLoadFromLookupRejectsInvalidDBMaxConns(t *testing.T) {
	tests := map[string]string{
		"not a number": "nope",
		"zero":         "0",
		"negative":     "-1",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromLookup(mapLookup(map[string]string{
				"DATABASE_URL": "postgres://user:pass@localhost:5432/app?sslmode=disable",
				"JWT_SECRET":   "dev-secret",
				"DB_MAX_CONNS": value,
			}))
			if err == nil {
				t.Fatal("expected invalid DB_MAX_CONNS to fail")
			}
		})
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
