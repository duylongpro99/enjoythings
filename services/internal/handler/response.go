package handler

import (
	"encoding/json"
	"net/http"
)

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, ErrorEnvelope{
		Error: APIError{
			Code:    code,
			Message: message,
		},
	})
}
