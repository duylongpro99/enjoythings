package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultAppEnv                = "local"
	defaultHTTPAddr              = ":8080"
	defaultGRPCAddr              = ":9090"
	defaultWalletGRPCAddr        = "127.0.0.1:9090"
	defaultLedgerGRPCAddr        = "127.0.0.1:9091"
	defaultLedgerServiceGRPCAddr = ":9091"
	defaultKafkaBrokers          = "127.0.0.1:9092"
	defaultWalletOutboxTopic     = "tx.initiated"
	defaultDBMaxConns            = 10
	defaultWalletOutboxBatchSize = 100
	defaultRateLimitBurst        = 60
	defaultRateLimitRefillEvery  = time.Second
	defaultWalletOutboxPollEvery = 100 * time.Millisecond
)

type Config struct {
	AppEnv                   string
	HTTPAddr                 string
	GRPCAddr                 string
	WalletGRPCAddr           string
	LedgerGRPCAddr           string
	DatabaseURL              string
	JWTSecret                string
	KafkaBrokers             string
	WalletOutboxTopic        string
	DBMaxConns               int32
	WalletOutboxBatchSize    int
	WalletOutboxPollInterval time.Duration
	RateLimitBurst           int
	RateLimitRefillEvery     time.Duration
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadWallet() (Config, error) {
	return LoadWalletFromLookup(os.LookupEnv)
}

func LoadLedger() (Config, error) {
	return LoadLedgerFromLookup(os.LookupEnv)
}

func LoadGateway() (Config, error) {
	return LoadGatewayFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup, defaultGRPCAddr)
	if err != nil {
		return Config{}, err
	}
	var ok bool
	cfg.JWTSecret, ok = lookup("JWT_SECRET")
	if !ok || cfg.JWTSecret == "" {
		return Config{}, errors.New("JWT_SECRET is required")
	}

	return cfg, nil
}

func LoadWalletFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup, defaultGRPCAddr)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.WalletOutboxTopic = valueOrDefault(lookup, "WALLET_OUTBOX_TOPIC", defaultWalletOutboxTopic)
	cfg.WalletOutboxPollInterval = defaultWalletOutboxPollEvery
	cfg.WalletOutboxBatchSize = defaultWalletOutboxBatchSize

	if raw, ok := lookup("WALLET_OUTBOX_POLL_INTERVAL"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("WALLET_OUTBOX_POLL_INTERVAL must be a positive duration")
		}
		cfg.WalletOutboxPollInterval = value
	}

	if raw, ok := lookup("WALLET_OUTBOX_BATCH_SIZE"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("WALLET_OUTBOX_BATCH_SIZE must be a positive integer")
		}
		cfg.WalletOutboxBatchSize = value
	}

	return cfg, nil
}

func LoadLedgerFromLookup(lookup func(string) (string, bool)) (Config, error) {
	return loadBase(lookup, defaultLedgerServiceGRPCAddr)
}

func LoadGatewayFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		AppEnv:               valueOrDefault(lookup, "APP_ENV", defaultAppEnv),
		HTTPAddr:             valueOrDefault(lookup, "HTTP_ADDR", defaultHTTPAddr),
		WalletGRPCAddr:       valueOrDefault(lookup, "WALLET_GRPC_ADDR", defaultWalletGRPCAddr),
		LedgerGRPCAddr:       valueOrDefault(lookup, "LEDGER_GRPC_ADDR", defaultLedgerGRPCAddr),
		RateLimitBurst:       defaultRateLimitBurst,
		RateLimitRefillEvery: defaultRateLimitRefillEvery,
	}

	var ok bool
	cfg.JWTSecret, ok = lookup("JWT_SECRET")
	if !ok || cfg.JWTSecret == "" {
		return Config{}, errors.New("JWT_SECRET is required")
	}

	if raw, ok := lookup("RATE_LIMIT_BURST"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("RATE_LIMIT_BURST must be a positive integer")
		}
		cfg.RateLimitBurst = value
	}

	if raw, ok := lookup("RATE_LIMIT_REFILL_EVERY"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("RATE_LIMIT_REFILL_EVERY must be a positive duration")
		}
		cfg.RateLimitRefillEvery = value
	}

	return cfg, nil
}

func loadBase(lookup func(string) (string, bool), defaultGRPC string) (Config, error) {
	cfg := Config{
		AppEnv:     valueOrDefault(lookup, "APP_ENV", defaultAppEnv),
		HTTPAddr:   valueOrDefault(lookup, "HTTP_ADDR", defaultHTTPAddr),
		GRPCAddr:   valueOrDefault(lookup, "GRPC_ADDR", defaultGRPC),
		DBMaxConns: defaultDBMaxConns,
	}

	var ok bool
	cfg.DatabaseURL, ok = lookup("DATABASE_URL")
	if !ok || cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	if raw, ok := lookup("DB_MAX_CONNS"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("DB_MAX_CONNS must be a positive integer")
		}
		cfg.DBMaxConns = int32(value)
	}

	return cfg, nil
}

func valueOrDefault(lookup func(string) (string, bool), key string, fallback string) string {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}
