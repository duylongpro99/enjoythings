package service

import (
	"net/http"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/handler"
	"enjoythings/services/internal/wallet"
)

func NewRouter(readiness handler.ReadinessChecker, store wallet.Store, jwtSecret string) http.Handler {
	walletService := wallet.NewService(store)
	business := http.NewServeMux()
	business.Handle("/v1/wallets", handler.NewWallets(walletService))
	business.Handle("/v1/wallets/", handler.NewWallets(walletService))
	business.Handle("/v1/transfers", handler.NewTransfers(walletService))
	business.Handle("/v1/ledger/", handler.NewLedger(walletService))
	business.Handle("/v1/", handler.NotFound())

	mux := http.NewServeMux()
	mux.Handle("/healthz", handler.Health())
	mux.Handle("/readyz", handler.Ready(readiness))
	mux.Handle("/v1/", auth.Middleware(jwtSecret)(business))
	return mux
}
