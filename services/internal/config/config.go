package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultAppEnv                      = "local"
	defaultHTTPAddr                    = ":8080"
	defaultGRPCAddr                    = ":9090"
	defaultWalletGRPCAddr              = "127.0.0.1:9090"
	defaultLedgerGRPCAddr              = "127.0.0.1:9091"
	defaultLedgerServiceGRPCAddr       = ":9091"
	defaultSagaGRPCAddr                = ":9093"
	defaultSagaClientGRPCAddr          = "127.0.0.1:9093"
	defaultVerificationGRPCAddr        = "127.0.0.1:9094"
	defaultVerificationServiceGRPCAddr = ":9094"
	defaultVerificationMode            = "auto"
	defaultKafkaBrokers                = "127.0.0.1:9092"
	defaultPaymentRailURL              = "http://127.0.0.1:18090"
	defaultWalletOutboxTopic           = "tx.initiated"
	defaultLedgerConsumerTopic         = "tx.initiated"
	defaultLedgerConsumerGroupID       = "ledger-service"
	defaultSagaConsumerGroupID         = "saga-orchestrator"
	defaultPaymentProcessorGroupID     = "payment-processor"
	defaultNotificationConsumerGroupID = "notification-service"
	defaultDBMaxConns                  = 10
	defaultWalletOutboxBatchSize       = 100
	defaultRateLimitBurst              = 60
	defaultRateLimitRefillEvery        = time.Second
	defaultWalletOutboxPollEvery       = 100 * time.Millisecond
	defaultPaymentRailTimeout          = 2 * time.Second
)

type Config struct {
	AppEnv                      string
	HTTPAddr                    string
	GRPCAddr                    string
	WalletGRPCAddr              string
	LedgerGRPCAddr              string
	SagaGRPCAddr                string
	VerificationGRPCAddr        string
	DatabaseURL                 string
	JWTSecret                   string
	KafkaBrokers                string
	WalletOutboxTopic           string
	LedgerConsumerTopic         string
	LedgerConsumerGroupID       string
	SagaConsumerGroupID         string
	PaymentProcessorGroupID     string
	NotificationConsumerGroupID string
	VerificationMode            string
	PaymentRailURL              string
	LedgerConsumerEnabled       bool
	SagaConsumerEnabled         bool
	DBMaxConns                  int32
	WalletOutboxBatchSize       int
	WalletOutboxPollInterval    time.Duration
	PaymentRailTimeout          time.Duration
	RateLimitBurst              int
	RateLimitRefillEvery        time.Duration
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

func LoadSaga() (Config, error) {
	return LoadSagaFromLookup(os.LookupEnv)
}

func LoadPaymentProcessor() (Config, error) {
	return LoadPaymentProcessorFromLookup(os.LookupEnv)
}

func LoadNotification() (Config, error) {
	return LoadNotificationFromLookup(os.LookupEnv)
}

func LoadVerification() (Config, error) {
	return LoadVerificationFromLookup(os.LookupEnv)
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
	cfg, err := loadBase(lookup, defaultLedgerServiceGRPCAddr)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.LedgerConsumerTopic = valueOrDefault(lookup, "LEDGER_CONSUMER_TOPIC", defaultLedgerConsumerTopic)
	cfg.LedgerConsumerGroupID = valueOrDefault(lookup, "LEDGER_CONSUMER_GROUP_ID", defaultLedgerConsumerGroupID)
	cfg.LedgerConsumerEnabled = true

	if raw, ok := lookup("LEDGER_CONSUMER_ENABLED"); ok && raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("LEDGER_CONSUMER_ENABLED must be a boolean")
		}
		cfg.LedgerConsumerEnabled = value
	}
	return cfg, nil
}

func LoadSagaFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup, defaultSagaGRPCAddr)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.WalletGRPCAddr = valueOrDefault(lookup, "WALLET_GRPC_ADDR", defaultWalletGRPCAddr)
	cfg.LedgerGRPCAddr = valueOrDefault(lookup, "LEDGER_GRPC_ADDR", defaultLedgerGRPCAddr)
	cfg.VerificationGRPCAddr = valueOrDefault(lookup, "VERIFICATION_GRPC_ADDR", defaultVerificationGRPCAddr)
	cfg.SagaConsumerGroupID = valueOrDefault(lookup, "SAGA_CONSUMER_GROUP_ID", defaultSagaConsumerGroupID)
	cfg.SagaConsumerEnabled = true
	cfg.WalletOutboxPollInterval = defaultWalletOutboxPollEvery
	cfg.WalletOutboxBatchSize = defaultWalletOutboxBatchSize

	if raw, ok := lookup("SAGA_CONSUMER_ENABLED"); ok && raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SAGA_CONSUMER_ENABLED must be a boolean")
		}
		cfg.SagaConsumerEnabled = value
	}
	if raw, ok := lookup("SAGA_OUTBOX_POLL_INTERVAL"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("SAGA_OUTBOX_POLL_INTERVAL must be a positive duration")
		}
		cfg.WalletOutboxPollInterval = value
	}
	if raw, ok := lookup("SAGA_OUTBOX_BATCH_SIZE"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("SAGA_OUTBOX_BATCH_SIZE must be a positive integer")
		}
		cfg.WalletOutboxBatchSize = value
	}
	return cfg, nil
}

func LoadPaymentProcessorFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup, defaultHTTPAddr)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.PaymentProcessorGroupID = valueOrDefault(lookup, "PAYMENT_PROCESSOR_CONSUMER_GROUP", defaultPaymentProcessorGroupID)
	cfg.PaymentRailURL = valueOrDefault(lookup, "PAYMENT_RAIL_URL", defaultPaymentRailURL)
	cfg.PaymentRailTimeout = defaultPaymentRailTimeout
	cfg.WalletOutboxPollInterval = defaultWalletOutboxPollEvery
	cfg.WalletOutboxBatchSize = defaultWalletOutboxBatchSize

	if raw, ok := lookup("PAYMENT_RAIL_TIMEOUT"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("PAYMENT_RAIL_TIMEOUT must be a positive duration")
		}
		cfg.PaymentRailTimeout = value
	}
	if raw, ok := lookup("PAYMENT_PROCESSOR_OUTBOX_INTERVAL"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("PAYMENT_PROCESSOR_OUTBOX_INTERVAL must be a positive duration")
		}
		cfg.WalletOutboxPollInterval = value
	}
	if raw, ok := lookup("PAYMENT_PROCESSOR_OUTBOX_BATCH_SIZE"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("PAYMENT_PROCESSOR_OUTBOX_BATCH_SIZE must be a positive integer")
		}
		cfg.WalletOutboxBatchSize = value
	}
	return cfg, nil
}

func LoadNotificationFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		AppEnv:     valueOrDefault(lookup, "APP_ENV", defaultAppEnv),
		HTTPAddr:   valueOrDefault(lookup, "HTTP_ADDR", defaultHTTPAddr),
		DBMaxConns: defaultDBMaxConns,
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.NotificationConsumerGroupID = valueOrDefault(lookup, "NOTIFICATION_CONSUMER_GROUP", defaultNotificationConsumerGroupID)
	return cfg, nil
}

func LoadVerificationFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup, defaultVerificationServiceGRPCAddr)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaBrokers = valueOrDefault(lookup, "KAFKA_BROKERS", defaultKafkaBrokers)
	cfg.VerificationMode = valueOrDefault(lookup, "VERIFICATION_MODE", defaultVerificationMode)
	cfg.WalletOutboxPollInterval = defaultWalletOutboxPollEvery
	cfg.WalletOutboxBatchSize = defaultWalletOutboxBatchSize

	if cfg.VerificationMode != "auto" && cfg.VerificationMode != "manual" && cfg.VerificationMode != "rules" {
		return Config{}, fmt.Errorf("VERIFICATION_MODE must be auto, manual, or rules")
	}
	if raw, ok := lookup("VERIFICATION_OUTBOX_INTERVAL"); ok && raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("VERIFICATION_OUTBOX_INTERVAL must be a positive duration")
		}
		cfg.WalletOutboxPollInterval = value
	}
	if raw, ok := lookup("VERIFICATION_OUTBOX_BATCH_SIZE"); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("VERIFICATION_OUTBOX_BATCH_SIZE must be a positive integer")
		}
		cfg.WalletOutboxBatchSize = value
	}
	return cfg, nil
}

func LoadGatewayFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		AppEnv:               valueOrDefault(lookup, "APP_ENV", defaultAppEnv),
		HTTPAddr:             valueOrDefault(lookup, "HTTP_ADDR", defaultHTTPAddr),
		WalletGRPCAddr:       valueOrDefault(lookup, "WALLET_GRPC_ADDR", defaultWalletGRPCAddr),
		LedgerGRPCAddr:       valueOrDefault(lookup, "LEDGER_GRPC_ADDR", defaultLedgerGRPCAddr),
		SagaGRPCAddr:         valueOrDefault(lookup, "SAGA_GRPC_ADDR", defaultSagaClientGRPCAddr),
		VerificationGRPCAddr: valueOrDefault(lookup, "VERIFICATION_GRPC_ADDR", defaultVerificationGRPCAddr),
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
