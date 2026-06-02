package service

import (
	"net/http"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/handler"
)

func NewRouter(readiness handler.ReadinessChecker, jwtSecret string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", handler.Health())
	mux.Handle("/readyz", handler.Ready(readiness))
	mux.Handle("/v1/", auth.Middleware(jwtSecret)(http.NotFoundHandler()))
	return mux
}
