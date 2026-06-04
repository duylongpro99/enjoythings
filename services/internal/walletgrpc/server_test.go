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

func TestGetWalletPassesMetadataUserIDAndMapsWallet(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 2, 0, 0, 0, time.UTC)
	app := &fakeApp{
		getWallet: domain.Wallet{ID: walletID, UserID: userID, Balance: 1200, Currency: "USD", CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Minute)},
	}
	server := NewServer(app)

	resp, err := server.GetWallet(contextWithUserID(userID), &walletv1.GetWalletRequest{
		WalletId: walletID.String(),
	})
	if err != nil {
		t.Fatalf("GetWallet error = %v", err)
	}

	if app.getWalletUserID != userID {
		t.Fatalf("GetWallet userID = %s, want %s", app.getWalletUserID, userID)
	}
	if resp.GetWallet().GetId() != walletID.String() {
		t.Fatalf("wallet id = %q, want %q", resp.GetWallet().GetId(), walletID.String())
	}
	if resp.GetWallet().GetBalanceCents() != 1200 {
		t.Fatalf("balance = %d, want 1200", resp.GetWallet().GetBalanceCents())
	}
	if resp.GetWallet().GetCreatedAt().AsTime() != createdAt {
		t.Fatalf("created_at = %s, want %s", resp.GetWallet().GetCreatedAt().AsTime(), createdAt)
	}
}

func TestGetWalletMapsDomainErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		code codes.Code
	}{
		"not found":              {err: domain.ErrNotFound, code: codes.NotFound},
		"unexpected persistence": {err: errors.New("connection refused"), code: codes.Internal},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{getWalletErr: tc.err})

			_, err := server.GetWallet(contextWithUserID(uuid.New()), &walletv1.GetWalletRequest{
				WalletId: uuid.NewString(),
			})

			assertCode(t, err, tc.code)
		})
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

func TestDebitForSagaValidatesRequest(t *testing.T) {
	tests := map[string]*walletv1.DebitForSagaRequest{
		"invalid payment":     {PaymentId: "bad", IdempotencyKey: "debit-key", FromWalletId: uuid.NewString(), AmountCents: 1, Currency: "USD"},
		"missing idempotency": {PaymentId: uuid.NewString(), FromWalletId: uuid.NewString(), AmountCents: 1, Currency: "USD"},
		"invalid wallet":      {PaymentId: uuid.NewString(), IdempotencyKey: "debit-key", FromWalletId: "bad", AmountCents: 1, Currency: "USD"},
		"non-positive amount": {PaymentId: uuid.NewString(), IdempotencyKey: "debit-key", FromWalletId: uuid.NewString(), AmountCents: 0, Currency: "USD"},
		"missing currency":    {PaymentId: uuid.NewString(), IdempotencyKey: "debit-key", FromWalletId: uuid.NewString(), AmountCents: 1},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{})

			_, err := server.DebitForSaga(context.Background(), req)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestDebitForSagaMapsResponseAndMetadataUserID(t *testing.T) {
	userID := uuid.New()
	paymentID := uuid.New()
	walletID := uuid.New()
	opID := uuid.New()
	app := &fakeApp{
		sagaDebit: domain.SagaWalletOperation{
			ID:                opID,
			PaymentID:         paymentID,
			Operation:         domain.SagaWalletOperationDebit,
			WalletID:          walletID,
			AmountCents:       1250,
			Currency:          "USD",
			Status:            domain.SagaWalletOperationCompleted,
			BalanceAfterCents: 3750,
		},
	}
	server := NewServer(app)

	resp, err := server.DebitForSaga(contextWithUserID(userID), &walletv1.DebitForSagaRequest{
		PaymentId:      paymentID.String(),
		IdempotencyKey: "debit-key",
		FromWalletId:   walletID.String(),
		AmountCents:    1250,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("DebitForSaga error = %v", err)
	}

	if app.sagaDebitUserID != userID {
		t.Fatalf("DebitForSaga userID = %s, want %s", app.sagaDebitUserID, userID)
	}
	if resp.GetWalletDebitId() != opID.String() || resp.GetPaymentId() != paymentID.String() {
		t.Fatalf("response IDs = %s/%s, want %s/%s", resp.GetWalletDebitId(), resp.GetPaymentId(), opID, paymentID)
	}
	if resp.GetStatus() != domain.SagaWalletOperationCompleted || resp.GetBalanceAfterCents() != 3750 {
		t.Fatalf("response = %+v, want completed balance 3750", resp)
	}
}

func TestDebitForSagaMapsInsufficientFunds(t *testing.T) {
	server := NewServer(&fakeApp{sagaDebitErr: domain.ErrInsufficientFunds})

	_, err := server.DebitForSaga(context.Background(), &walletv1.DebitForSagaRequest{
		PaymentId:      uuid.NewString(),
		IdempotencyKey: "debit-key",
		FromWalletId:   uuid.NewString(),
		AmountCents:    1250,
		Currency:       "USD",
	})

	assertCode(t, err, codes.FailedPrecondition)
}

func TestCompensateDebitValidatesRequest(t *testing.T) {
	tests := map[string]*walletv1.CompensateDebitRequest{
		"invalid payment":     {PaymentId: "bad", IdempotencyKey: "comp-key", FromWalletId: uuid.NewString()},
		"missing idempotency": {PaymentId: uuid.NewString(), FromWalletId: uuid.NewString()},
		"invalid wallet":      {PaymentId: uuid.NewString(), IdempotencyKey: "comp-key", FromWalletId: "bad"},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{})

			_, err := server.CompensateDebit(context.Background(), req)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestCompensateDebitMapsResponseAndMissingDebit(t *testing.T) {
	paymentID := uuid.New()
	walletID := uuid.New()
	opID := uuid.New()
	app := &fakeApp{
		sagaCompensation: domain.SagaWalletOperation{
			ID:                opID,
			PaymentID:         paymentID,
			Operation:         domain.SagaWalletOperationCompensation,
			WalletID:          walletID,
			Status:            domain.SagaWalletOperationCompleted,
			BalanceAfterCents: 5000,
		},
	}
	server := NewServer(app)

	resp, err := server.CompensateDebit(context.Background(), &walletv1.CompensateDebitRequest{
		PaymentId:      paymentID.String(),
		IdempotencyKey: "comp-key",
		FromWalletId:   walletID.String(),
	})
	if err != nil {
		t.Fatalf("CompensateDebit error = %v", err)
	}
	if resp.GetCompensationId() != opID.String() || resp.GetBalanceAfterCents() != 5000 {
		t.Fatalf("response = %+v, want compensation %s balance 5000", resp, opID)
	}

	_, err = NewServer(&fakeApp{sagaCompensationErr: domain.ErrDebitNotFound}).CompensateDebit(context.Background(), &walletv1.CompensateDebitRequest{
		PaymentId:      paymentID.String(),
		IdempotencyKey: "comp-key",
		FromWalletId:   walletID.String(),
	})
	assertCode(t, err, codes.FailedPrecondition)
}

type fakeApp struct {
	createWallet domain.Wallet
	createErr    error
	createUserID uuid.UUID

	getWallet       domain.Wallet
	getWalletErr    error
	getWalletUserID uuid.UUID

	balanceWallet domain.Wallet
	balanceErr    error
	balanceUserID uuid.UUID

	transfer       domain.Transfer
	transferErr    error
	transferUserID uuid.UUID

	sagaDebit        domain.SagaWalletOperation
	sagaDebitErr     error
	sagaDebitUserID  uuid.UUID
	sagaDebitCommand domain.SagaDebitCommand

	sagaCompensation        domain.SagaWalletOperation
	sagaCompensationErr     error
	sagaCompensationCommand domain.SagaCompensationCommand
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

func (app *fakeApp) GetWallet(_ context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	app.getWalletUserID = userID
	if app.getWalletErr != nil {
		return domain.Wallet{}, app.getWalletErr
	}
	wallet := app.getWallet
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

func (app *fakeApp) DebitForSaga(_ context.Context, userID uuid.UUID, cmd domain.SagaDebitCommand) (domain.SagaWalletOperation, error) {
	app.sagaDebitUserID = userID
	app.sagaDebitCommand = cmd
	if app.sagaDebitErr != nil {
		return domain.SagaWalletOperation{}, app.sagaDebitErr
	}
	return app.sagaDebit, nil
}

func (app *fakeApp) CompensateDebit(_ context.Context, cmd domain.SagaCompensationCommand) (domain.SagaWalletOperation, error) {
	app.sagaCompensationCommand = cmd
	if app.sagaCompensationErr != nil {
		return domain.SagaWalletOperation{}, app.sagaCompensationErr
	}
	return app.sagaCompensation, nil
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
