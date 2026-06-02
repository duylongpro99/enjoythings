package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/config"
	gatewayclient "enjoythings/services/internal/gateway/client"
	gatewayhandler "enjoythings/services/internal/gateway/handler"
	gatewaymiddleware "enjoythings/services/internal/gateway/middleware"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := grpc.NewClient(cfg.WalletGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	walletClient := gatewayclient.NewWalletClient(walletv1.NewWalletServiceClient(conn))
	routes := http.NewServeMux()
	routes.Handle("/v1/wallets", gatewayhandler.NewWallets(walletClient))
	routes.Handle("/v1/wallets/", gatewayhandler.NewWallets(walletClient))
	routes.Handle("/v1/transfers", gatewayhandler.NewTransfers(walletClient))

	limiter := gatewaymiddleware.NewRateLimiter(cfg.RateLimitBurst, cfg.RateLimitRefillEvery)
	handler := auth.Middleware(cfg.JWTSecret)(limiter.Middleware(routes))
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("gateway listening", "addr", cfg.HTTPAddr, "wallet_grpc_addr", cfg.WalletGRPCAddr, "env", cfg.AppEnv)
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
