package client

import (
	"context"
	"testing"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLedgerClientMapsEntriesAndMetadata(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	entryID := uuid.New()
	transferID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 7, 0, 0, 0, time.UTC)
	next := repo.LedgerCursor{CreatedAt: createdAt, ID: entryID, Valid: true}
	grpcClient := &fakeLedgerServiceClient{
		response: &ledgerv1.GetEntriesResponse{
			WalletId:   walletID.String(),
			NextCursor: EncodeLedgerCursor(next),
			Entries: []*ledgerv1.LedgerEntry{{
				Id:                entryID.String(),
				TransferId:        transferID.String(),
				Direction:         "credit",
				AmountCents:       100,
				BalanceAfterCents: 1100,
				CreatedAt:         timestamppb.New(createdAt),
			}},
		},
	}
	client := NewLedgerClient(grpcClient)

	entries, gotNext, err := client.ListLedger(context.Background(), userID, walletID, repo.LedgerCursor{}, 25)
	if err != nil {
		t.Fatalf("ListLedger error = %v", err)
	}

	if grpcClient.outgoingUserID != userID.String() {
		t.Fatalf("x-user-id metadata = %q, want %q", grpcClient.outgoingUserID, userID.String())
	}
	if grpcClient.request.GetWalletId() != walletID.String() || grpcClient.request.GetLimit() != 25 {
		t.Fatalf("request = %+v", grpcClient.request)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ID != entryID || entry.WalletID != walletID || entry.TransferID != transferID || entry.Direction != "credit" || entry.Amount != 100 || entry.BalanceAfter != 1100 || !entry.CreatedAt.Equal(createdAt) {
		t.Fatalf("entry = %+v", entry)
	}
	if gotNext != next {
		t.Fatalf("next cursor = %s, want %s", gotNext, next)
	}
}

type fakeLedgerServiceClient struct {
	request        *ledgerv1.GetEntriesRequest
	response       *ledgerv1.GetEntriesResponse
	outgoingUserID string
	err            error
}

func (client *fakeLedgerServiceClient) GetEntries(ctx context.Context, req *ledgerv1.GetEntriesRequest, _ ...grpc.CallOption) (*ledgerv1.GetEntriesResponse, error) {
	client.request = req
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		values := md.Get("x-user-id")
		if len(values) > 0 {
			client.outgoingUserID = values[0]
		}
	}
	return client.response, client.err
}
