package config

import (
	"testing"
	"time"
)

func TestLoadSagaFromLookupLeavesTheReviewReaperOffByDefault(t *testing.T) {
	cfg, err := LoadSagaFromLookup(mapLookup(map[string]string{
		"DATABASE_URL": "postgres://localhost/enjoythings",
	}))
	if err != nil {
		t.Fatalf("LoadSagaFromLookup: %v", err)
	}
	if cfg.FraudReviewTTL != 0 {
		t.Fatalf("FraudReviewTTL = %s, want 0 (disabled)", cfg.FraudReviewTTL)
	}
	if cfg.FraudReviewReaperInterval != time.Minute {
		t.Fatalf("FraudReviewReaperInterval = %s, want 1m", cfg.FraudReviewReaperInterval)
	}
}

func TestLoadSagaFromLookupReadsTheReviewReaperSettings(t *testing.T) {
	cfg, err := LoadSagaFromLookup(mapLookup(map[string]string{
		"DATABASE_URL":                      "postgres://localhost/enjoythings",
		"SAGA_FRAUD_REVIEW_TTL":             "24h",
		"SAGA_FRAUD_REVIEW_REAPER_INTERVAL": "5m",
	}))
	if err != nil {
		t.Fatalf("LoadSagaFromLookup: %v", err)
	}
	if cfg.FraudReviewTTL != 24*time.Hour || cfg.FraudReviewReaperInterval != 5*time.Minute {
		t.Fatalf("reaper settings = %s/%s, want 24h/5m", cfg.FraudReviewTTL, cfg.FraudReviewReaperInterval)
	}
}

func TestLoadSagaFromLookupRejectsInvalidReviewReaperSettings(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"negative ttl":       {"SAGA_FRAUD_REVIEW_TTL": "-1h"},
		"malformed ttl":      {"SAGA_FRAUD_REVIEW_TTL": "tomorrow"},
		"zero interval":      {"SAGA_FRAUD_REVIEW_REAPER_INTERVAL": "0s"},
		"malformed interval": {"SAGA_FRAUD_REVIEW_REAPER_INTERVAL": "often"},
	} {
		values["DATABASE_URL"] = "postgres://localhost/enjoythings"
		if _, err := LoadSagaFromLookup(mapLookup(values)); err == nil {
			t.Fatalf("%s: LoadSagaFromLookup accepted %v", name, values)
		}
	}
}

func TestLoadSagaFromLookupKeepsFraudAuditForeverByDefault(t *testing.T) {
	tests := map[string]map[string]string{
		"unset":    {},
		"empty":    {"SAGA_FRAUD_AUDIT_RETENTION": ""},
		"explicit": {"SAGA_FRAUD_AUDIT_RETENTION": "0"},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			values["DATABASE_URL"] = "postgres://user:pass@localhost:5432/app?sslmode=disable"
			cfg, err := LoadSagaFromLookup(mapLookup(values))
			if err != nil {
				t.Fatalf("load saga config: %v", err)
			}
			if cfg.SagaFraudAuditRetention != 0 {
				t.Fatalf("SagaFraudAuditRetention = %s, want 0 (keep forever)", cfg.SagaFraudAuditRetention)
			}
		})
	}
}

func TestLoadSagaFromLookupParsesFraudAuditRetention(t *testing.T) {
	cfg, err := LoadSagaFromLookup(mapLookup(map[string]string{
		"DATABASE_URL":               "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"SAGA_FRAUD_AUDIT_RETENTION": "2160h",
	}))
	if err != nil {
		t.Fatalf("load saga config: %v", err)
	}
	if cfg.SagaFraudAuditRetention != 90*24*time.Hour {
		t.Fatalf("SagaFraudAuditRetention = %s, want 2160h", cfg.SagaFraudAuditRetention)
	}
}

func TestLoadSagaFromLookupRejectsInvalidFraudAuditRetention(t *testing.T) {
	tests := map[string]string{
		"not a duration": "30 days",
		"negative":       "-1h",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadSagaFromLookup(mapLookup(map[string]string{
				"DATABASE_URL":               "postgres://user:pass@localhost:5432/app?sslmode=disable",
				"SAGA_FRAUD_AUDIT_RETENTION": value,
			}))
			if err == nil {
				t.Fatal("expected invalid SAGA_FRAUD_AUDIT_RETENTION to fail")
			}
		})
	}
}
