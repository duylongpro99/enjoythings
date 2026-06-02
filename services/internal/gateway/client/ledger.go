package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
)

type LedgerClient struct {
	client ledgerv1.LedgerServiceClient
}

func NewLedgerClient(client ledgerv1.LedgerServiceClient) *LedgerClient {
	return &LedgerClient{client: client}
}

func (client *LedgerClient) ListLedger(ctx context.Context, userID, walletID uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	resp, err := client.client.GetEntries(contextWithUserID(ctx, userID), &ledgerv1.GetEntriesRequest{
		WalletId: walletID.String(),
		Limit:    int32(limit),
		Cursor:   EncodeLedgerCursor(cursor),
	})
	if err != nil {
		return nil, repo.LedgerCursor{}, err
	}
	next, err := DecodeLedgerCursor(resp.GetNextCursor())
	if err != nil {
		return nil, repo.LedgerCursor{}, err
	}
	entries := make([]domain.LedgerEntry, 0, len(resp.GetEntries()))
	for _, message := range resp.GetEntries() {
		entry, err := ledgerEntryFromMessage(resp.GetWalletId(), message)
		if err != nil {
			return nil, repo.LedgerCursor{}, err
		}
		entries = append(entries, entry)
	}
	return entries, next, nil
}

func ledgerEntryFromMessage(walletIDRaw string, message *ledgerv1.LedgerEntry) (domain.LedgerEntry, error) {
	if message == nil {
		return domain.LedgerEntry{}, fmt.Errorf("ledger grpc response missing entry")
	}
	walletID, err := uuid.Parse(walletIDRaw)
	if err != nil {
		return domain.LedgerEntry{}, fmt.Errorf("ledger grpc wallet_id is invalid: %w", err)
	}
	id, err := uuid.Parse(message.GetId())
	if err != nil {
		return domain.LedgerEntry{}, fmt.Errorf("ledger grpc id is invalid: %w", err)
	}
	transferID, err := uuid.Parse(message.GetTransferId())
	if err != nil {
		return domain.LedgerEntry{}, fmt.Errorf("ledger grpc transfer_id is invalid: %w", err)
	}
	entry := domain.LedgerEntry{
		ID:           id,
		WalletID:     walletID,
		TransferID:   transferID,
		Direction:    message.GetDirection(),
		Amount:       message.GetAmountCents(),
		BalanceAfter: message.GetBalanceAfterCents(),
	}
	if message.GetCreatedAt() != nil {
		entry.CreatedAt = message.GetCreatedAt().AsTime()
	}
	return entry, nil
}

func EncodeLedgerCursor(cursor repo.LedgerCursor) string {
	if !cursor.Valid {
		return ""
	}
	payload, _ := json.Marshal(map[string]string{
		"created_at": cursor.CreatedAt.Format(time.RFC3339Nano),
		"id":         cursor.ID.String(),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeLedgerCursor(raw string) (repo.LedgerCursor, error) {
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
