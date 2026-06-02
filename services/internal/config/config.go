package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

const (
	defaultAppEnv     = "local"
	defaultHTTPAddr   = ":8080"
	defaultGRPCAddr   = ":9090"
	defaultDBMaxConns = 10
)

type Config struct {
	AppEnv      string
	HTTPAddr    string
	GRPCAddr    string
	DatabaseURL string
	JWTSecret   string
	DBMaxConns  int32
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadWallet() (Config, error) {
	return LoadWalletFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg, err := loadBase(lookup)
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
	return loadBase(lookup)
}

func loadBase(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		AppEnv:     valueOrDefault(lookup, "APP_ENV", defaultAppEnv),
		HTTPAddr:   valueOrDefault(lookup, "HTTP_ADDR", defaultHTTPAddr),
		GRPCAddr:   valueOrDefault(lookup, "GRPC_ADDR", defaultGRPCAddr),
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
