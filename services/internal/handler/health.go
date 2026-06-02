package handler

import (
	"context"
	"net/http"
)

type ReadinessChecker interface {
	Ping(context.Context) error
}

func Health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

func Ready(checker ReadinessChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := checker.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable", "database is not ready")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
}
