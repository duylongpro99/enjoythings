package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
)

type WalletService interface {
	CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error)
	GetWallet(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	GetBalance(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error)
	ListLedger(context.Context, uuid.UUID, uuid.UUID, repo.LedgerCursor, int) ([]domain.LedgerEntry, repo.LedgerCursor, error)
}

func NewWallets(service WalletService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/v1/wallets" {
			if rejectNonJSONContentType(w, r) {
				return
			}
			var request struct {
				Currency string `json:"currency"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
				return
			}
			wallet, err := service.CreateWallet(r.Context(), principal.UserID, request.Currency)
			if err != nil {
				writeDomainError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, walletResponse(wallet))
			return
		}

		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/wallets/") {
			rest := strings.TrimPrefix(r.URL.Path, "/v1/wallets/")
			isBalance := strings.HasSuffix(rest, "/balance")
			if isBalance {
				rest = strings.TrimSuffix(rest, "/balance")
			}
			walletID, err := uuid.Parse(rest)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "wallet id is invalid")
				return
			}
			wallet, err := service.GetWallet(r.Context(), principal.UserID, walletID)
			if isBalance {
				wallet, err = service.GetBalance(r.Context(), principal.UserID, walletID)
			}
			if err != nil {
				writeDomainError(w, err)
				return
			}
			if isBalance {
				writeJSON(w, http.StatusOK, map[string]any{
					"wallet_id": wallet.ID,
					"balance":   wallet.Balance,
					"currency":  wallet.Currency,
				})
				return
			}
			writeJSON(w, http.StatusOK, walletResponse(wallet))
			return
		}

		writeNotFound(w)
	})
}

func NewTransfers(service WalletService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/transfers" {
			writeNotFound(w)
			return
		}
		if rejectNonJSONContentType(w, r) {
			return
		}
		var request struct {
			FromWalletID string `json:"from_wallet_id"`
			ToWalletID   string `json:"to_wallet_id"`
			Amount       int64  `json:"amount"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
			return
		}
		fromWalletID, err := uuid.Parse(request.FromWalletID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "from wallet id is invalid")
			return
		}
		toWalletID, err := uuid.Parse(request.ToWalletID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "to wallet id is invalid")
			return
		}
		if request.Amount <= 0 {
			writeDomainError(w, domain.ErrInvalidAmount)
			return
		}
		if fromWalletID == toWalletID {
			writeDomainError(w, domain.ErrInvalidTransfer)
			return
		}
		transfer, err := service.CreateTransfer(r.Context(), principal.UserID, fromWalletID, toWalletID, request.Amount)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, transferResponse(transfer))
	})
}

func NewLedger(service WalletService) http.Handler {
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
		entries, next, err := service.ListLedger(r.Context(), principal.UserID, walletID, cursor, limit)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ledgerResponse(walletID, entries, next))
	})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "wallet_not_found", "wallet not found")
	case errors.Is(err, domain.ErrUnsupportedCurrency):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_currency", "currency is unsupported")
	case errors.Is(err, domain.ErrInvalidAmount):
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount", "amount is invalid")
	case errors.Is(err, domain.ErrInvalidTransfer):
		writeError(w, http.StatusUnprocessableEntity, "invalid_transfer", "transfer is invalid")
	case errors.Is(err, domain.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_funds", "insufficient funds")
	case errors.Is(err, domain.ErrCurrencyMismatch):
		writeError(w, http.StatusUnprocessableEntity, "currency_mismatch", "currency mismatch")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func walletResponse(wallet domain.Wallet) map[string]any {
	return map[string]any{
		"id":         wallet.ID,
		"user_id":    wallet.UserID,
		"balance":    wallet.Balance,
		"currency":   wallet.Currency,
		"created_at": wallet.CreatedAt,
		"updated_at": wallet.UpdatedAt,
	}
}

func transferResponse(transfer domain.Transfer) map[string]any {
	return map[string]any{
		"id":             transfer.ID,
		"from_wallet_id": transfer.FromWalletID,
		"to_wallet_id":   transfer.ToWalletID,
		"amount":         transfer.Amount,
		"status":         transfer.Status,
		"created_at":     transfer.CreatedAt,
		"balances": map[string]int64{
			"from": transfer.FromBalance,
			"to":   transfer.ToBalance,
		},
	}
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
