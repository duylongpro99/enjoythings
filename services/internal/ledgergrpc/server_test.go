package ledgergrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGetEntriesMapsValidQuery(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	entryID := uuid.New()
	transferID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 4, 0, 0, 0, time.UTC)
	next := repo.LedgerCursor{CreatedAt: createdAt, ID: entryID, Valid: true}
	app := &fakeLedgerApp{
		entries: []domain.LedgerEntry{{
			ID:           entryID,
			WalletID:     walletID,
			TransferID:   transferID,
			Direction:    "debit",
			Amount:       250,
			BalanceAfter: 750,
			CreatedAt:    createdAt,
		}},
		next: next,
	}
	server := NewServer(app)

	resp, err := server.GetEntries(contextWithUserID(userID), &ledgerv1.GetEntriesRequest{
		WalletId: walletID.String(),
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("GetEntries error = %v", err)
	}

	if app.userID != userID {
		t.Fatalf("userID = %s, want %s", app.userID, userID)
	}
	if app.walletID != walletID {
		t.Fatalf("walletID = %s, want %s", app.walletID, walletID)
	}
	if app.limit != 1 {
		t.Fatalf("limit = %d, want 1", app.limit)
	}
	if resp.GetWalletId() != walletID.String() {
		t.Fatalf("wallet_id = %q, want %q", resp.GetWalletId(), walletID.String())
	}
	if len(resp.GetEntries()) != 1 {
		t.Fatalf("entries len = %d, want 1", len(resp.GetEntries()))
	}
	got := resp.GetEntries()[0]
	if got.GetId() != entryID.String() || got.GetTransferId() != transferID.String() || got.GetDirection() != "debit" || got.GetAmountCents() != 250 || got.GetBalanceAfterCents() != 750 || !got.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Fatalf("entry = %+v", got)
	}
	if resp.GetNextCursor() == "" {
		t.Fatal("next_cursor is empty")
	}
	decoded, err := DecodeCursor(resp.GetNextCursor())
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	if decoded != next {
		t.Fatalf("next cursor = %s, want %s", decoded, next)
	}
}

func TestGetEntriesValidatesRequest(t *testing.T) {
	server := NewServer(&fakeLedgerApp{})

	tests := map[string]*ledgerv1.GetEntriesRequest{
		"nil request":       nil,
		"invalid wallet id": {WalletId: "not-a-uuid"},
		"invalid limit":     {WalletId: uuid.NewString(), Limit: 101},
		"invalid cursor":    {WalletId: uuid.NewString(), Cursor: "not-base64url"},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := server.GetEntries(contextWithUserID(uuid.New()), req)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGetEntriesRequiresUserMetadata(t *testing.T) {
	server := NewServer(&fakeLedgerApp{})

	_, err := server.GetEntries(context.Background(), &ledgerv1.GetEntriesRequest{WalletId: uuid.NewString()})

	assertCode(t, err, codes.InvalidArgument)
}

func TestGetEntriesMapsErrorsAndEmptyResult(t *testing.T) {
	tests := map[string]struct {
		err  error
		code codes.Code
	}{
		"missing wallet":          {err: domain.ErrNotFound, code: codes.NotFound},
		"invalid pagination":      {err: domain.ErrInvalidAmount, code: codes.InvalidArgument},
		"unexpected persistence":  {err: errors.New("database unavailable"), code: codes.Internal},
		"empty result is success": {err: nil, code: codes.OK},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeLedgerApp{err: tc.err})

			resp, err := server.GetEntries(contextWithUserID(uuid.New()), &ledgerv1.GetEntriesRequest{WalletId: uuid.NewString()})

			if tc.code == codes.OK {
				if err != nil {
					t.Fatalf("GetEntries error = %v", err)
				}
				if len(resp.GetEntries()) != 0 {
					t.Fatalf("entries len = %d, want 0", len(resp.GetEntries()))
				}
				return
			}
			assertCode(t, err, tc.code)
		})
	}
}

type fakeLedgerApp struct {
	entries  []domain.LedgerEntry
	next     repo.LedgerCursor
	err      error
	userID   uuid.UUID
	walletID uuid.UUID
	cursor   repo.LedgerCursor
	limit    int
}

func (app *fakeLedgerApp) ListLedger(_ context.Context, userID, walletID uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	app.userID = userID
	app.walletID = walletID
	app.cursor = cursor
	app.limit = limit
	if app.err != nil {
		return nil, repo.LedgerCursor{}, app.err
	}
	return app.entries, app.next, nil
}

func contextWithUserID(userID uuid.UUID) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-user-id", userID.String()))
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if status.Code(err) != want {
		t.Fatalf("code = %s, want %s (err %v)", status.Code(err), want, err)
	}
}
