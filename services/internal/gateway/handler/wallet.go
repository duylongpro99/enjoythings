package handler

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/domain"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type WalletClient interface {
	CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error)
	GetWallet(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	GetBalance(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error)
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewWallets(client WalletClient) http.Handler {
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
			wallet, err := client.CreateWallet(r.Context(), principal.UserID, request.Currency)
			if err != nil {
				writeGRPCError(w, err)
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
			var wallet domain.Wallet
			if isBalance {
				wallet, err = client.GetBalance(r.Context(), principal.UserID, walletID)
			} else {
				wallet, err = client.GetWallet(r.Context(), principal.UserID, walletID)
			}
			if err != nil {
				writeGRPCError(w, err)
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

func NewTransfers(client WalletClient) http.Handler {
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
			writeError(w, http.StatusUnprocessableEntity, "invalid_amount", "amount is invalid")
			return
		}
		if fromWalletID == toWalletID {
			writeError(w, http.StatusUnprocessableEntity, "invalid_transfer", "transfer is invalid")
			return
		}
		transfer, err := client.CreateTransfer(r.Context(), principal.UserID, fromWalletID, toWalletID, request.Amount)
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, transferResponse(transfer))
	})
}

func writeGRPCError(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeError(w, http.StatusBadRequest, "invalid_request", status.Convert(err).Message())
	case codes.NotFound:
		writeError(w, http.StatusNotFound, "wallet_not_found", "wallet not found")
	case codes.FailedPrecondition:
		if status.Convert(err).Message() == "insufficient funds" {
			writeError(w, http.StatusUnprocessableEntity, "insufficient_funds", "insufficient funds")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "failed_precondition", status.Convert(err).Message())
	case codes.Internal:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, ErrorEnvelope{Error: APIError{Code: code, Message: message}})
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
