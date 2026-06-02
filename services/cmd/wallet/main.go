package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/config"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/wallet"
	"enjoythings/services/internal/walletgrpc"

	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("wallet stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWallet()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := repo.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer db.Close()

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	walletv1.RegisterWalletServiceServer(grpcServer, walletgrpc.NewServer(wallet.NewService(db)))

	errCh := make(chan error, 1)
	go func() {
		slog.Info("wallet grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		errCh <- grpcServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		return nil
	case err := <-errCh:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	}
}
