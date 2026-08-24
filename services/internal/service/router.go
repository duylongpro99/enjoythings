package service

import (
	"net/http"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/handler"
	"enjoythings/services/internal/telemetry"
	"enjoythings/services/internal/wallet"
)

// NewRouter wires the wallet service HTTP surface. The verifier decides which
// token algorithm the business routes accept.
func NewRouter(readiness handler.ReadinessChecker, store wallet.Store, verifier auth.Verifier) http.Handler {
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
	mux.Handle("/v1/", auth.VerifierMiddleware(verifier)(business))
	return telemetry.InstrumentHTTP("api", mux)
}
