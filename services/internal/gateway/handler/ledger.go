package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
)

type LedgerClient interface {
	ListLedger(context.Context, uuid.UUID, uuid.UUID, repo.LedgerCursor, int) ([]domain.LedgerEntry, repo.LedgerCursor, error)
}

func NewLedger(client LedgerClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/ledger/") {
			writeNotFound(w)
			return
		}
		walletID, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/v1/ledger/"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "wallet id is invalid")
			return
		}
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 100 {
				writeError(w, http.StatusBadRequest, "invalid_request", "limit is invalid")
				return
			}
		}
		cursor, err := decodeLedgerCursor(r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
			return
		}
		entries, next, err := client.ListLedger(r.Context(), principal.UserID, walletID, cursor, limit)
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ledgerResponse(walletID, entries, next))
	})
}

func ledgerResponse(walletID uuid.UUID, entries []domain.LedgerEntry, next repo.LedgerCursor) map[string]any {
	responseEntries := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		responseEntries = append(responseEntries, map[string]any{
			"id":            entry.ID,
			"transfer_id":   entry.TransferID,
			"direction":     entry.Direction,
			"amount":        entry.Amount,
			"balance_after": entry.BalanceAfter,
			"created_at":    entry.CreatedAt,
		})
	}
	var nextCursor any
	if next.Valid {
		nextCursor = encodeLedgerCursor(next)
	}
	return map[string]any{
		"wallet_id":   walletID,
		"entries":     responseEntries,
		"next_cursor": nextCursor,
	}
}

func encodeLedgerCursor(cursor repo.LedgerCursor) string {
	payload, _ := json.Marshal(map[string]string{
		"created_at": cursor.CreatedAt.Format(time.RFC3339Nano),
		"id":         cursor.ID.String(),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeLedgerCursor(raw string) (repo.LedgerCursor, error) {
	if raw == "" {
		return repo.LedgerCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	var decoded struct {
		CreatedAt string `json:"created_at"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return repo.LedgerCursor{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, decoded.CreatedAt)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	id, err := uuid.Parse(decoded.ID)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	return repo.LedgerCursor{CreatedAt: createdAt, ID: id, Valid: true}, nil
}
