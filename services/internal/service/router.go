package service

import (
	"net/http"

	"enjoythings/services/internal/handler"
)

func NewRouter(readiness handler.ReadinessChecker) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", handler.Health())
	mux.Handle("/readyz", handler.Ready(readiness))
	return mux
}
