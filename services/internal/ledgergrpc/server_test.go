package ledgergrpc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
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

func TestGetFraudTransactionHistoryReturnsSanitizedSummaries(t *testing.T) {
	walletID := uuid.New()
	createdAt := time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)
	app := &fakeLedgerApp{
		fraudHistory: []domain.FraudTransactionSummary{{
			Direction:   "debit",
			AmountCents: 1250,
			Currency:    "USD",
			OccurredAt:  createdAt,
		}},
	}
	server := NewServer(app)

	resp, err := server.GetFraudTransactionHistory(context.Background(), &ledgerv1.GetFraudTransactionHistoryRequest{
		WalletId: walletID.String(),
		Limit:    20,
		TraceId:  "trace-1",
	})
	if err != nil {
		t.Fatalf("GetFraudTransactionHistory: %v", err)
	}

	if app.fraudWalletID != walletID || app.fraudLimit != 20 || app.fraudTraceID != "trace-1" {
		t.Fatalf("fraud history request = wallet:%s limit:%d trace:%s", app.fraudWalletID, app.fraudLimit, app.fraudTraceID)
	}
	if len(resp.GetEntries()) != 1 {
		t.Fatalf("entries len = %d, want 1", len(resp.GetEntries()))
	}
	entry := resp.GetEntries()[0]
	if entry.GetDirection() != "debit" || entry.GetAmountCents() != 1250 || entry.GetCurrency() != "USD" || !entry.GetOccurredAt().AsTime().Equal(createdAt) {
		t.Fatalf("sanitized entry = %+v", entry)
	}
}

func TestGetFraudVelocityMetricsReturnsAggregates(t *testing.T) {
	walletID := uuid.New()
	before := time.Now().UTC()
	app := &fakeLedgerApp{
		fraudVelocity: domain.FraudVelocityMetrics{
			TransactionsLastHour:  3,
			AmountLastHourCents:   4000,
			AverageAmount30dCents: 900,
			DistinctRecipients30d: 2,
		},
	}
	server := NewServer(app)

	resp, err := server.GetFraudVelocityMetrics(context.Background(), &ledgerv1.GetFraudVelocityMetricsRequest{
		WalletId: walletID.String(),
		TraceId:  "trace-2",
	})
	if err != nil {
		t.Fatalf("GetFraudVelocityMetrics: %v", err)
	}

	if app.fraudWalletID != walletID || app.fraudTraceID != "trace-2" {
		t.Fatalf("fraud velocity request = wallet:%s trace:%s", app.fraudWalletID, app.fraudTraceID)
	}
	after := time.Now().UTC()
	if app.fraudAsOf.Before(before) || app.fraudAsOf.After(after) || app.fraudAsOf.Location() != time.UTC {
		t.Fatalf("fraud as_of = %v, want one UTC timestamp captured by handler between %v and %v", app.fraudAsOf, before, after)
	}
	if resp.GetTransactionsLastHour() != 3 || resp.GetAmountLastHourCents() != 4000 || resp.GetAverageAmount_30DCents() != 900 || resp.GetDistinctRecipients_30D() != 2 {
		t.Fatalf("velocity response = %+v", resp)
	}
}

func TestFraudEnrichmentResponseSchemasContainOnlySanitizedFields(t *testing.T) {
	tests := []struct {
		name   string
		fields protoreflect.FieldDescriptors
		want   []string
	}{
		{
			name:   "history response",
			fields: (&ledgerv1.GetFraudTransactionHistoryResponse{}).ProtoReflect().Descriptor().Fields(),
			want:   []string{"entries"},
		},
		{
			name:   "history entry",
			fields: (&ledgerv1.FraudTransactionHistoryEntry{}).ProtoReflect().Descriptor().Fields(),
			want:   []string{"direction", "amount_cents", "currency", "occurred_at"},
		},
		{
			name:   "velocity response",
			fields: (&ledgerv1.GetFraudVelocityMetricsResponse{}).ProtoReflect().Descriptor().Fields(),
			want: []string{
				"transactions_last_hour",
				"amount_last_hour_cents",
				"average_amount_30d_cents",
				"distinct_recipients_30d",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := make([]string, 0, test.fields.Len())
			for index := 0; index < test.fields.Len(); index++ {
				got = append(got, string(test.fields.Get(index).Name()))
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("fields = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFraudEnrichmentRequestsValidateInput(t *testing.T) {
	server := NewServer(&fakeLedgerApp{})

	_, err := server.GetFraudTransactionHistory(context.Background(), &ledgerv1.GetFraudTransactionHistoryRequest{WalletId: uuid.NewString(), Limit: 101})
	assertCode(t, err, codes.InvalidArgument)

	_, err = server.GetFraudTransactionHistory(context.Background(), &ledgerv1.GetFraudTransactionHistoryRequest{WalletId: "not-a-uuid", Limit: 20})
	assertCode(t, err, codes.InvalidArgument)

	_, err = server.GetFraudVelocityMetrics(context.Background(), &ledgerv1.GetFraudVelocityMetricsRequest{WalletId: "not-a-uuid"})
	assertCode(t, err, codes.InvalidArgument)
}

func TestReserveTransferMapsValidCommand(t *testing.T) {
	paymentID := uuid.New()
	fromWalletID := uuid.New()
	toWalletID := uuid.New()
	reservationID := uuid.New()
	app := &fakeLedgerApp{
		reservation: domain.LedgerReservation{
			ID:        reservationID,
			PaymentID: paymentID,
			Status:    domain.LedgerReservationReserved,
		},
	}
	server := NewServer(app)

	resp, err := server.ReserveTransfer(context.Background(), &ledgerv1.ReserveTransferRequest{
		PaymentId:      paymentID.String(),
		IdempotencyKey: "reserve-key",
		TraceId:        "trace-1",
		FromWalletId:   fromWalletID.String(),
		ToWalletId:     toWalletID.String(),
		AmountCents:    1250,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("ReserveTransfer error = %v", err)
	}

	if app.reserveCmd.PaymentID != paymentID || app.reserveCmd.FromWalletID != fromWalletID || app.reserveCmd.ToWalletID != toWalletID {
		t.Fatalf("reserve command IDs = %+v", app.reserveCmd)
	}
	if app.reserveCmd.IdempotencyKey != "reserve-key" || app.reserveCmd.TraceID != "trace-1" || app.reserveCmd.AmountCents != 1250 || app.reserveCmd.Currency != "USD" {
		t.Fatalf("reserve command payload = %+v", app.reserveCmd)
	}
	if resp.GetPaymentId() != paymentID.String() || resp.GetLedgerReservationId() != reservationID.String() || resp.GetStatus() != domain.LedgerReservationReserved {
		t.Fatalf("response = %+v", resp)
	}
}

func TestConfirmTransferMapsValidCommand(t *testing.T) {
	paymentID := uuid.New()
	reservationID := uuid.New()
	walletDebitID := uuid.New()
	transferID := uuid.New()
	completedAt := time.Date(2026, 6, 4, 1, 2, 3, 0, time.UTC)
	app := &fakeLedgerApp{
		confirmation: domain.LedgerConfirmation{
			PaymentID:   paymentID,
			TransferID:  transferID,
			Status:      domain.LedgerReservationConfirmed,
			CompletedAt: completedAt,
		},
	}
	server := NewServer(app)

	resp, err := server.ConfirmTransfer(context.Background(), &ledgerv1.ConfirmTransferRequest{
		PaymentId:           paymentID.String(),
		IdempotencyKey:      "confirm-key",
		TraceId:             "trace-2",
		LedgerReservationId: reservationID.String(),
		WalletDebitId:       walletDebitID.String(),
	})
	if err != nil {
		t.Fatalf("ConfirmTransfer error = %v", err)
	}

	if app.confirmCmd.PaymentID != paymentID || app.confirmCmd.LedgerReservationID != reservationID || app.confirmCmd.WalletDebitID != walletDebitID {
		t.Fatalf("confirm command IDs = %+v", app.confirmCmd)
	}
	if app.confirmCmd.IdempotencyKey != "confirm-key" || app.confirmCmd.TraceID != "trace-2" {
		t.Fatalf("confirm command payload = %+v", app.confirmCmd)
	}
	if resp.GetPaymentId() != paymentID.String() || resp.GetTransferId() != transferID.String() || resp.GetStatus() != domain.LedgerReservationConfirmed || !resp.GetCompletedAt().AsTime().Equal(completedAt) {
		t.Fatalf("response = %+v", resp)
	}
}

func TestCancelReservationMapsValidCommand(t *testing.T) {
	paymentID := uuid.New()
	reservationID := uuid.New()
	app := &fakeLedgerApp{
		cancellation: domain.LedgerReservation{
			ID:        reservationID,
			PaymentID: paymentID,
			Status:    domain.LedgerReservationCanceled,
		},
	}
	server := NewServer(app)

	resp, err := server.CancelReservation(context.Background(), &ledgerv1.CancelReservationRequest{
		PaymentId:           paymentID.String(),
		IdempotencyKey:      "cancel-key",
		TraceId:             "trace-3",
		LedgerReservationId: reservationID.String(),
		Reason:              "payment failed",
	})
	if err != nil {
		t.Fatalf("CancelReservation error = %v", err)
	}

	if app.cancelCmd.PaymentID != paymentID || app.cancelCmd.LedgerReservationID != reservationID {
		t.Fatalf("cancel command IDs = %+v", app.cancelCmd)
	}
	if app.cancelCmd.IdempotencyKey != "cancel-key" || app.cancelCmd.TraceID != "trace-3" || app.cancelCmd.Reason != "payment failed" {
		t.Fatalf("cancel command payload = %+v", app.cancelCmd)
	}
	if resp.GetPaymentId() != paymentID.String() || resp.GetLedgerReservationId() != reservationID.String() || resp.GetStatus() != domain.LedgerReservationCanceled {
		t.Fatalf("response = %+v", resp)
	}
}

func TestLedgerSagaCommandsValidateRequests(t *testing.T) {
	server := NewServer(&fakeLedgerApp{})

	tests := map[string]struct {
		call func() error
	}{
		"nil reserve": {
			call: func() error {
				_, err := server.ReserveTransfer(context.Background(), nil)
				return err
			},
		},
		"reserve missing idempotency": {
			call: func() error {
				_, err := server.ReserveTransfer(context.Background(), &ledgerv1.ReserveTransferRequest{
					PaymentId:    uuid.NewString(),
					FromWalletId: uuid.NewString(),
					ToWalletId:   uuid.NewString(),
					AmountCents:  100,
					Currency:     "USD",
				})
				return err
			},
		},
		"reserve same wallet": {
			call: func() error {
				walletID := uuid.NewString()
				_, err := server.ReserveTransfer(context.Background(), &ledgerv1.ReserveTransferRequest{
					PaymentId:      uuid.NewString(),
					IdempotencyKey: "reserve-key",
					FromWalletId:   walletID,
					ToWalletId:     walletID,
					AmountCents:    100,
					Currency:       "USD",
				})
				return err
			},
		},
		"nil confirm": {
			call: func() error {
				_, err := server.ConfirmTransfer(context.Background(), nil)
				return err
			},
		},
		"confirm invalid reservation id": {
			call: func() error {
				_, err := server.ConfirmTransfer(context.Background(), &ledgerv1.ConfirmTransferRequest{
					PaymentId:           uuid.NewString(),
					IdempotencyKey:      "confirm-key",
					LedgerReservationId: "not-a-uuid",
				})
				return err
			},
		},
		"nil cancel": {
			call: func() error {
				_, err := server.CancelReservation(context.Background(), nil)
				return err
			},
		},
		"cancel missing idempotency": {
			call: func() error {
				_, err := server.CancelReservation(context.Background(), &ledgerv1.CancelReservationRequest{
					PaymentId:           uuid.NewString(),
					LedgerReservationId: uuid.NewString(),
				})
				return err
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assertCode(t, tc.call(), codes.InvalidArgument)
		})
	}
}

func TestLedgerSagaCommandsMapDomainErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		code codes.Code
		call func(*Server) error
	}{
		"already exists": {
			err:  domain.ErrAlreadyExists,
			code: codes.AlreadyExists,
			call: func(server *Server) error {
				_, err := server.ReserveTransfer(context.Background(), validReserveRequest())
				return err
			},
		},
		"failed precondition": {
			err:  domain.ErrFailedPrecondition,
			code: codes.FailedPrecondition,
			call: func(server *Server) error {
				_, err := server.ConfirmTransfer(context.Background(), validConfirmRequest())
				return err
			},
		},
		"not found": {
			err:  domain.ErrNotFound,
			code: codes.NotFound,
			call: func(server *Server) error {
				_, err := server.CancelReservation(context.Background(), validCancelRequest())
				return err
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeLedgerApp{err: tc.err})

			assertCode(t, tc.call(server), tc.code)
		})
	}
}

type fakeLedgerApp struct {
	entries       []domain.LedgerEntry
	next          repo.LedgerCursor
	err           error
	userID        uuid.UUID
	walletID      uuid.UUID
	cursor        repo.LedgerCursor
	limit         int
	reserveCmd    domain.LedgerReserveCommand
	confirmCmd    domain.LedgerConfirmCommand
	cancelCmd     domain.LedgerCancelCommand
	reservation   domain.LedgerReservation
	confirmation  domain.LedgerConfirmation
	cancellation  domain.LedgerReservation
	fraudHistory  []domain.FraudTransactionSummary
	fraudVelocity domain.FraudVelocityMetrics
	fraudWalletID uuid.UUID
	fraudLimit    int
	fraudTraceID  string
	fraudAsOf     time.Time
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

func (app *fakeLedgerApp) GetFraudTransactionHistory(_ context.Context, walletID uuid.UUID, limit int, traceID string) ([]domain.FraudTransactionSummary, error) {
	app.fraudWalletID = walletID
	app.fraudLimit = limit
	app.fraudTraceID = traceID
	if app.err != nil {
		return nil, app.err
	}
	return app.fraudHistory, nil
}

func (app *fakeLedgerApp) GetFraudVelocityMetrics(_ context.Context, walletID uuid.UUID, asOf time.Time, traceID string) (domain.FraudVelocityMetrics, error) {
	app.fraudWalletID = walletID
	app.fraudTraceID = traceID
	app.fraudAsOf = asOf
	if app.err != nil {
		return domain.FraudVelocityMetrics{}, app.err
	}
	return app.fraudVelocity, nil
}

func (app *fakeLedgerApp) ReserveTransfer(_ context.Context, cmd domain.LedgerReserveCommand) (domain.LedgerReservation, error) {
	app.reserveCmd = cmd
	if app.err != nil {
		return domain.LedgerReservation{}, app.err
	}
	return app.reservation, nil
}

func (app *fakeLedgerApp) ConfirmTransfer(_ context.Context, cmd domain.LedgerConfirmCommand) (domain.LedgerConfirmation, error) {
	app.confirmCmd = cmd
	if app.err != nil {
		return domain.LedgerConfirmation{}, app.err
	}
	return app.confirmation, nil
}

func (app *fakeLedgerApp) CancelReservation(_ context.Context, cmd domain.LedgerCancelCommand) (domain.LedgerReservation, error) {
	app.cancelCmd = cmd
	if app.err != nil {
		return domain.LedgerReservation{}, app.err
	}
	return app.cancellation, nil
}

func validReserveRequest() *ledgerv1.ReserveTransferRequest {
	return &ledgerv1.ReserveTransferRequest{
		PaymentId:      uuid.NewString(),
		IdempotencyKey: "reserve-key",
		FromWalletId:   uuid.NewString(),
		ToWalletId:     uuid.NewString(),
		AmountCents:    100,
		Currency:       "USD",
	}
}

func validConfirmRequest() *ledgerv1.ConfirmTransferRequest {
	return &ledgerv1.ConfirmTransferRequest{
		PaymentId:           uuid.NewString(),
		IdempotencyKey:      "confirm-key",
		LedgerReservationId: uuid.NewString(),
		WalletDebitId:       uuid.NewString(),
	}
}

func validCancelRequest() *ledgerv1.CancelReservationRequest {
	return &ledgerv1.CancelReservationRequest{
		PaymentId:           uuid.NewString(),
		IdempotencyKey:      "cancel-key",
		LedgerReservationId: uuid.NewString(),
	}
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
