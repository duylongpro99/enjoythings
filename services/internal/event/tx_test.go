package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTransactionInitiatedJSONSchema(t *testing.T) {
	initiatedAt := time.Date(2026, 6, 2, 12, 34, 56, 0, time.UTC)
	event := TransactionInitiated{
		TransferID:   "transfer-123",
		FromWalletID: "wallet-from",
		ToWalletID:   "wallet-to",
		AmountCents:  1250,
		Currency:     "USD",
		InitiatedAt:  initiatedAt,
	}

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal TransactionInitiated: %v", err)
	}

	const want = `{"transfer_id":"transfer-123","from_wallet_id":"wallet-from","to_wallet_id":"wallet-to","amount_cents":1250,"currency":"USD","initiated_at":"2026-06-02T12:34:56Z"}`
	if string(body) != want {
		t.Fatalf("unexpected JSON\nwant: %s\n got: %s", want, body)
	}

	var decoded TransactionInitiated
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal TransactionInitiated: %v", err)
	}

	if !decoded.InitiatedAt.Equal(initiatedAt) {
		t.Fatalf("initiated_at mismatch: want %s got %s", initiatedAt, decoded.InitiatedAt)
	}

	if decoded.TransferID != event.TransferID ||
		decoded.FromWalletID != event.FromWalletID ||
		decoded.ToWalletID != event.ToWalletID ||
		decoded.AmountCents != event.AmountCents ||
		decoded.Currency != event.Currency {
		t.Fatalf("decoded event mismatch: %#v", decoded)
	}
}
