package event

import (
	"encoding/json"
	"time"

	"enjoythings/services/internal/domain"
)

const TransactionInitiatedTopic = "tx.initiated"

// TransactionInitiated is the Phase 2 tx.initiated Kafka JSON event.
type TransactionInitiated struct {
	TransferID   string    `json:"transfer_id"`
	FromWalletID string    `json:"from_wallet_id"`
	ToWalletID   string    `json:"to_wallet_id"`
	AmountCents  int64     `json:"amount_cents"`
	Currency     string    `json:"currency"`
	InitiatedAt  time.Time `json:"initiated_at"`
}

func MarshalTransactionInitiated(transfer domain.Transfer, currency string) ([]byte, error) {
	return json.Marshal(TransactionInitiated{
		TransferID:   transfer.ID.String(),
		FromWalletID: transfer.FromWalletID.String(),
		ToWalletID:   transfer.ToWalletID.String(),
		AmountCents:  transfer.Amount,
		Currency:     currency,
		InitiatedAt:  transfer.CreatedAt,
	})
}
