package walletgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/domain"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestCreateWalletValidatesUserID(t *testing.T) {
	server := NewServer(&fakeApp{})

	_, err := server.CreateWallet(context.Background(), &walletv1.CreateWalletRequest{
		UserId:   "not-a-uuid",
		Currency: "USD",
	})

	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateWalletMapsWalletResponse(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	app := &fakeApp{
		createWallet: domain.Wallet{
			ID:        walletID,
			UserID:    userID,
			Balance:   1250,
			Currency:  "USD",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}
	server := NewServer(app)

	resp, err := server.CreateWallet(context.Background(), &walletv1.CreateWalletRequest{
		UserId:   userID.String(),
		Currency: "usd",
	})
	if err != nil {
		t.Fatalf("CreateWallet error = %v", err)
	}

	if app.createUserID != userID {
		t.Fatalf("CreateWallet userID = %s, want %s", app.createUserID, userID)
	}
	if resp.GetWallet().GetId() != walletID.String() {
		t.Fatalf("wallet id = %q, want %q", resp.GetWallet().GetId(), walletID.String())
	}
	if resp.GetWallet().GetBalanceCents() != 1250 {
		t.Fatalf("balance = %d, want 1250", resp.GetWallet().GetBalanceCents())
	}
	if resp.GetWallet().GetCreatedAt().AsTime() != createdAt {
		t.Fatalf("created_at = %s, want %s", resp.GetWallet().GetCreatedAt().AsTime(), createdAt)
	}
}

func TestGetBalanceRequiresUserMetadata(t *testing.T) {
	server := NewServer(&fakeApp{})

	_, err := server.GetBalance(context.Background(), &walletv1.GetBalanceRequest{
		WalletId: uuid.NewString(),
	})

	assertCode(t, err, codes.InvalidArgument)
}

func TestGetBalanceMapsDomainErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		code codes.Code
	}{
		"not found":              {err: domain.ErrNotFound, code: codes.NotFound},
		"invalid amount":         {err: domain.ErrInvalidAmount, code: codes.InvalidArgument},
		"unsupported currency":   {err: domain.ErrUnsupportedCurrency, code: codes.InvalidArgument},
		"invalid transfer":       {err: domain.ErrInvalidTransfer, code: codes.InvalidArgument},
		"insufficient funds":     {err: domain.ErrInsufficientFunds, code: codes.FailedPrecondition},
		"currency mismatch":      {err: domain.ErrCurrencyMismatch, code: codes.InvalidArgument},
		"unexpected persistence": {err: errors.New("connection refused"), code: codes.Internal},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{balanceErr: tc.err})
			ctx := contextWithUserID(uuid.New())

			_, err := server.GetBalance(ctx, &walletv1.GetBalanceRequest{
				WalletId: uuid.NewString(),
			})

			assertCode(t, err, tc.code)
		})
	}
}

func TestGetBalancePassesMetadataUserID(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	app := &fakeApp{
		balanceWallet: domain.Wallet{ID: walletID, UserID: userID, Balance: 500, Currency: "USD"},
	}
	server := NewServer(app)

	resp, err := server.GetBalance(contextWithUserID(userID), &walletv1.GetBalanceRequest{
		WalletId: walletID.String(),
	})
	if err != nil {
		t.Fatalf("GetBalance error = %v", err)
	}

	if app.balanceUserID != userID {
		t.Fatalf("GetBalance userID = %s, want %s", app.balanceUserID, userID)
	}
	if resp.GetWalletId() != walletID.String() {
		t.Fatalf("wallet_id = %q, want %q", resp.GetWalletId(), walletID.String())
	}
	if resp.GetBalanceCents() != 500 {
		t.Fatalf("balance_cents = %d, want 500", resp.GetBalanceCents())
	}
}

func TestInitiateTransferValidatesRequest(t *testing.T) {
	tests := map[string]*walletv1.InitiateTransferRequest{
		"invalid from wallet": {FromWalletId: "bad", ToWalletId: uuid.NewString(), AmountCents: 1},
		"invalid to wallet":   {FromWalletId: uuid.NewString(), ToWalletId: "bad", AmountCents: 1},
		"non-positive amount": {FromWalletId: uuid.NewString(), ToWalletId: uuid.NewString(), AmountCents: 0},
		"same wallet": func() *walletv1.InitiateTransferRequest {
			id := uuid.NewString()
			return &walletv1.InitiateTransferRequest{FromWalletId: id, ToWalletId: id, AmountCents: 1}
		}(),
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{})

			_, err := server.InitiateTransfer(contextWithUserID(uuid.New()), req)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestInitiateTransferMapsResponse(t *testing.T) {
	userID := uuid.New()
	fromWalletID := uuid.New()
	toWalletID := uuid.New()
	transferID := uuid.New()
	app := &fakeApp{
		transfer: domain.Transfer{
			ID:           transferID,
			FromWalletID: fromWalletID,
			ToWalletID:   toWalletID,
			Amount:       250,
			Status:       "posted",
			FromBalance:  750,
			ToBalance:    1250,
		},
	}
	server := NewServer(app)

	resp, err := server.InitiateTransfer(contextWithUserID(userID), &walletv1.InitiateTransferRequest{
		FromWalletId: fromWalletID.String(),
		ToWalletId:   toWalletID.String(),
		AmountCents:  250,
	})
	if err != nil {
		t.Fatalf("InitiateTransfer error = %v", err)
	}

	if app.transferUserID != userID {
		t.Fatalf("InitiateTransfer userID = %s, want %s", app.transferUserID, userID)
	}
	if resp.GetTransferId() != transferID.String() {
		t.Fatalf("transfer_id = %q, want %q", resp.GetTransferId(), transferID.String())
	}
	if resp.GetBalances().GetFromBalanceCents() != 750 {
		t.Fatalf("from balance = %d, want 750", resp.GetBalances().GetFromBalanceCents())
	}
	if resp.GetBalances().GetToBalanceCents() != 1250 {
		t.Fatalf("to balance = %d, want 1250", resp.GetBalances().GetToBalanceCents())
	}
}

type fakeApp struct {
	createWallet domain.Wallet
	createErr    error
	createUserID uuid.UUID

	balanceWallet domain.Wallet
	balanceErr    error
	balanceUserID uuid.UUID

	transfer       domain.Transfer
	transferErr    error
	transferUserID uuid.UUID
}

func (app *fakeApp) CreateWallet(_ context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	app.createUserID = userID
	if app.createErr != nil {
		return domain.Wallet{}, app.createErr
	}
	wallet := app.createWallet
	wallet.UserID = userID
	wallet.Currency = currency
	return wallet, nil
}

func (app *fakeApp) GetBalance(_ context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	app.balanceUserID = userID
	if app.balanceErr != nil {
		return domain.Wallet{}, app.balanceErr
	}
	wallet := app.balanceWallet
	wallet.ID = walletID
	wallet.UserID = userID
	return wallet, nil
}

func (app *fakeApp) CreateTransfer(_ context.Context, userID, fromWalletID, toWalletID uuid.UUID, amount int64) (domain.Transfer, error) {
	app.transferUserID = userID
	if app.transferErr != nil {
		return domain.Transfer{}, app.transferErr
	}
	transfer := app.transfer
	transfer.FromWalletID = fromWalletID
	transfer.ToWalletID = toWalletID
	transfer.Amount = amount
	return transfer, nil
}

func contextWithUserID(userID uuid.UUID) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(userIDMetadataKey, userID.String()))
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %s", want)
	}
	if got := status.Code(err); got != want {
		t.Fatalf("code = %s, want %s; err = %v", got, want, err)
	}
}
