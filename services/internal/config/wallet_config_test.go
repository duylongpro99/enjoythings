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
	if cfg.KafkaBrokers != "127.0.0.1:9092" {
		t.Fatalf("KafkaBrokers = %q, want 127.0.0.1:9092", cfg.KafkaBrokers)
	}
	if cfg.WalletOutboxTopic != "tx.initiated" {
		t.Fatalf("WalletOutboxTopic = %q, want tx.initiated", cfg.WalletOutboxTopic)
	}
	if cfg.WalletOutboxPollInterval.String() != "100ms" {
		t.Fatalf("WalletOutboxPollInterval = %s, want 100ms", cfg.WalletOutboxPollInterval)
	}
	if cfg.WalletOutboxBatchSize != 100 {
		t.Fatalf("WalletOutboxBatchSize = %d, want 100", cfg.WalletOutboxBatchSize)
	}
}

func TestLoadWalletFromLookupUsesCustomValues(t *testing.T) {
	cfg, err := LoadWalletFromLookup(mapLookup(map[string]string{
		"APP_ENV":                     "test",
		"GRPC_ADDR":                   ":19090",
		"DATABASE_URL":                "postgres://user:pass@localhost:5432/app?sslmode=disable",
		"DB_MAX_CONNS":                "25",
		"KAFKA_BROKERS":               "kafka:9092,localhost:9093",
		"WALLET_OUTBOX_TOPIC":         "custom.tx.initiated",
		"WALLET_OUTBOX_POLL_INTERVAL": "250ms",
		"WALLET_OUTBOX_BATCH_SIZE":    "25",
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
	if cfg.KafkaBrokers != "kafka:9092,localhost:9093" {
		t.Fatalf("KafkaBrokers = %q, want custom brokers", cfg.KafkaBrokers)
	}
	if cfg.WalletOutboxTopic != "custom.tx.initiated" {
		t.Fatalf("WalletOutboxTopic = %q, want custom.tx.initiated", cfg.WalletOutboxTopic)
	}
	if cfg.WalletOutboxPollInterval.String() != "250ms" {
		t.Fatalf("WalletOutboxPollInterval = %s, want 250ms", cfg.WalletOutboxPollInterval)
	}
	if cfg.WalletOutboxBatchSize != 25 {
		t.Fatalf("WalletOutboxBatchSize = %d, want 25", cfg.WalletOutboxBatchSize)
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

func TestLoadWalletFromLookupRejectsInvalidOutboxSettings(t *testing.T) {
	tests := map[string]map[string]string{
		"invalid poll interval": {
			"DATABASE_URL":                "postgres://user:pass@localhost:5432/app?sslmode=disable",
			"WALLET_OUTBOX_POLL_INTERVAL": "0s",
		},
		"invalid batch size": {
			"DATABASE_URL":             "postgres://user:pass@localhost:5432/app?sslmode=disable",
			"WALLET_OUTBOX_BATCH_SIZE": "0",
		},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadWalletFromLookup(mapLookup(values))
			if err == nil {
				t.Fatal("expected invalid wallet outbox config to fail")
			}
		})
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
	if !cfg.LedgerConsumerEnabled {
		t.Fatal("LedgerConsumerEnabled = false, want true")
	}
	if cfg.KafkaBrokers != "127.0.0.1:9092" {
		t.Fatalf("KafkaBrokers = %q, want 127.0.0.1:9092", cfg.KafkaBrokers)
	}
	if cfg.LedgerConsumerTopic != "tx.initiated" {
		t.Fatalf("LedgerConsumerTopic = %q, want tx.initiated", cfg.LedgerConsumerTopic)
	}
	if cfg.LedgerConsumerGroupID != "ledger-service" {
		t.Fatalf("LedgerConsumerGroupID = %q, want ledger-service", cfg.LedgerConsumerGroupID)
	}
}

func TestLoadLedgerFromLookupUsesCustomConsumerSettings(t *testing.T) {
	cfg, err := LoadLedgerFromLookup(mapLookup(map[string]string{
		"DATABASE_URL":             "postgres://user:pass@localhost:5432/ledger?sslmode=disable",
		"KAFKA_BROKERS":            "kafka:9092,localhost:9093",
		"LEDGER_CONSUMER_TOPIC":    "custom.tx.initiated",
		"LEDGER_CONSUMER_GROUP_ID": "custom-ledger",
		"LEDGER_CONSUMER_ENABLED":  "false",
	}))
	if err != nil {
		t.Fatalf("load ledger config: %v", err)
	}

	if cfg.KafkaBrokers != "kafka:9092,localhost:9093" {
		t.Fatalf("KafkaBrokers = %q, want custom brokers", cfg.KafkaBrokers)
	}
	if cfg.LedgerConsumerTopic != "custom.tx.initiated" {
		t.Fatalf("LedgerConsumerTopic = %q, want custom.tx.initiated", cfg.LedgerConsumerTopic)
	}
	if cfg.LedgerConsumerGroupID != "custom-ledger" {
		t.Fatalf("LedgerConsumerGroupID = %q, want custom-ledger", cfg.LedgerConsumerGroupID)
	}
	if cfg.LedgerConsumerEnabled {
		t.Fatal("LedgerConsumerEnabled = true, want false")
	}
}

func TestLoadLedgerFromLookupRejectsInvalidConsumerEnabled(t *testing.T) {
	_, err := LoadLedgerFromLookup(mapLookup(map[string]string{
		"DATABASE_URL":            "postgres://user:pass@localhost:5432/ledger?sslmode=disable",
		"LEDGER_CONSUMER_ENABLED": "sometimes",
	}))
	if err == nil {
		t.Fatal("expected invalid LEDGER_CONSUMER_ENABLED to fail")
	}
}
