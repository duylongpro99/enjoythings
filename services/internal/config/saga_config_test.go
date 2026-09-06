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
