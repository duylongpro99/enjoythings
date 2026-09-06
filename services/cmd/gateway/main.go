package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	sagav1 "enjoythings/services/gen/saga/v1"
	verificationv1 "enjoythings/services/gen/verification/v1"
	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/config"
	gatewayclient "enjoythings/services/internal/gateway/client"
	gatewayhandler "enjoythings/services/internal/gateway/handler"
	gatewaymiddleware "enjoythings/services/internal/gateway/middleware"
	"enjoythings/services/internal/mtls"
	"enjoythings/services/internal/telemetry"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadGateway()
	if err != nil {
		return err
	}

	verifier, err := auth.NewVerifier(cfg.JWTAlgorithm, cfg.JWTSecret, cfg.JWTPublicKeyPEM)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.Init(ctx, "gateway", cfg.AppEnv)
	if err != nil {
		slog.Warn("telemetry init failed", "error", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	clientCreds, err := mtls.ClientCredentials(cfg.MTLS())
	if err != nil {
		return err
	}

	walletConn, err := grpc.NewClient(cfg.WalletGRPCAddr, grpc.WithTransportCredentials(clientCreds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return err
	}
	defer func() { _ = walletConn.Close() }()

	ledgerConn, err := grpc.NewClient(cfg.LedgerGRPCAddr, grpc.WithTransportCredentials(clientCreds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return err
	}
	defer func() { _ = ledgerConn.Close() }()
	sagaConn, err := grpc.NewClient(cfg.SagaGRPCAddr, grpc.WithTransportCredentials(clientCreds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return err
	}
	defer func() { _ = sagaConn.Close() }()
	verificationConn, err := grpc.NewClient(cfg.VerificationGRPCAddr, grpc.WithTransportCredentials(clientCreds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return err
	}
	defer func() { _ = verificationConn.Close() }()

	walletClient := gatewayclient.NewWalletClient(walletv1.NewWalletServiceClient(walletConn))
	ledgerClient := gatewayclient.NewLedgerClient(ledgerv1.NewLedgerServiceClient(ledgerConn))
	sagaClient := gatewayclient.NewSagaClient(sagav1.NewSagaServiceClient(sagaConn))
	verificationClient := gatewayclient.NewVerificationClient(verificationv1.NewVerificationServiceClient(verificationConn))
	routes := http.NewServeMux()
	routes.Handle("/v1/wallets", gatewayhandler.NewWallets(walletClient))
	routes.Handle("/v1/wallets/", gatewayhandler.NewWallets(walletClient))
	routes.Handle("/v1/transfers", gatewayhandler.NewPayments(sagaClient))
	routes.Handle("/v1/payments/", gatewayhandler.NewPayments(sagaClient))
	routes.Handle("/v1/fraud-reviews", gatewayhandler.NewFraudReviews(sagaClient))
	routes.Handle("/v1/fraud-reviews/", gatewayhandler.NewFraudReviews(sagaClient))
	routes.Handle("/v1/ledger/", gatewayhandler.NewLedger(ledgerClient))
	routes.Handle("/v1/verification/submit", gatewayhandler.NewVerification(verificationClient))
	routes.Handle("/v1/verification/status", gatewayhandler.NewVerification(verificationClient))

	limiter := gatewaymiddleware.NewRateLimiter(cfg.RateLimitBurst, cfg.RateLimitRefillEvery)
	protected := auth.VerifierMiddleware(verifier)(limiter.Middleware(routes))
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		writeStatus(w, "ok")
	})
	handler.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if err := dialReady(r.Context(), cfg.WalletGRPCAddr, cfg.LedgerGRPCAddr, cfg.SagaGRPCAddr, cfg.VerificationGRPCAddr); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"service_unavailable","message":"upstream service is not ready"}}` + "\n"))
			return
		}
		writeStatus(w, "ready")
	})
	handler.Handle("/", protected)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           otelhttp.NewHandler(telemetry.InstrumentHTTP("gateway", handler), "gateway.http"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("gateway listening", "addr", cfg.HTTPAddr, "wallet_grpc_addr", cfg.WalletGRPCAddr, "ledger_grpc_addr", cfg.LedgerGRPCAddr, "saga_grpc_addr", cfg.SagaGRPCAddr, "verification_grpc_addr", cfg.VerificationGRPCAddr, "env", cfg.AppEnv)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func dialReady(ctx context.Context, addrs ...string) error {
	dialer := net.Dialer{Timeout: time.Second}
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		_ = conn.Close()
	}
	return nil
}

func writeStatus(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"` + status + `"}` + "\n"))
}
