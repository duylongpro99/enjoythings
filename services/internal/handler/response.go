package handler

import (
	"encoding/json"
	"mime"
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

func writeNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func rejectNonJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && mediaType == "application/json" {
		return false
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "content type must be application/json")
	return true
}

func NotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeNotFound(w)
	})
}
