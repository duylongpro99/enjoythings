package event

import "time"

// TransactionInitiated is the Phase 2 tx.initiated Kafka JSON event.
type TransactionInitiated struct {
	TransferID   string    `json:"transfer_id"`
	FromWalletID string    `json:"from_wallet_id"`
	ToWalletID   string    `json:"to_wallet_id"`
	AmountCents  int64     `json:"amount_cents"`
	Currency     string    `json:"currency"`
	InitiatedAt  time.Time `json:"initiated_at"`
}
