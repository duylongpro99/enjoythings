package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/config"
	"enjoythings/services/internal/ledgergrpc"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/wallet"

	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ledger stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadLedger()
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
	ledgerv1.RegisterLedgerServiceServer(grpcServer, ledgergrpc.NewServer(wallet.NewService(db)))

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ledger grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
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
