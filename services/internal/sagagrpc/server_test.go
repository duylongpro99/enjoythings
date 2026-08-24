package sagagrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var fixedTime = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

func TestStartPaymentSagaMapsRequestAndResponse(t *testing.T) {
	app := &fakeApp{started: saga.Saga{
		PaymentID:    "payment-1",
		State:        saga.StatePaymentProcessing,
		FromWalletID: "from-1",
		ToWalletID:   "to-1",
		AmountCents:  1250,
		Currency:     "USD",
		CreatedAt:    fixedTime,
		UpdatedAt:    fixedTime,
	}}
	server := NewServer(app)

	resp, err := server.StartPaymentSaga(context.Background(), &sagav1.StartPaymentSagaRequest{
		PaymentId:      "payment-1",
		IdempotencyKey: "idem-1",
		TraceId:        "trace-1",
		UserId:         "user-1",
		FromWalletId:   "from-1",
		ToWalletId:     "to-1",
		AmountCents:    1250,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}
	if app.startedReq.PaymentID != "payment-1" || app.startedReq.IdempotencyKey != "idem-1" || app.startedReq.UserID != "user-1" {
		t.Fatalf("request = %+v", app.startedReq)
	}
	if resp.GetPaymentId() != "payment-1" || resp.GetStatus() != saga.StatePaymentProcessing || resp.GetAcceptedAt() == nil {
		t.Fatalf("response = %+v", resp)
	}
}

func TestGetPaymentSagaMapsSagaResponse(t *testing.T) {
	app := &fakeApp{got: saga.Saga{
		PaymentID:    "payment-1",
		State:        saga.StateCompleted,
		FromWalletID: "from-1",
		ToWalletID:   "to-1",
		AmountCents:  1250,
		Currency:     "USD",
		CreatedAt:    fixedTime,
		UpdatedAt:    fixedTime,
	}}
	server := NewServer(app)

	resp, err := server.GetPaymentSaga(context.Background(), &sagav1.GetPaymentSagaRequest{PaymentId: "payment-1"})
	if err != nil {
		t.Fatalf("GetPaymentSaga: %v", err)
	}
	got := resp.GetSaga()
	if got.GetPaymentId() != "payment-1" || got.GetStatus() != saga.StateCompleted || got.GetAmountCents() != 1250 {
		t.Fatalf("saga response = %+v", got)
	}
}

func TestStartPaymentSagaMapsAlreadyExists(t *testing.T) {
	server := NewServer(&fakeApp{err: saga.ErrAlreadyExists})

	_, err := server.StartPaymentSaga(context.Background(), validStartRequest())
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("code = %s, want %s: %v", status.Code(err), codes.AlreadyExists, err)
	}
}

func TestGetPaymentSagaMapsNotFound(t *testing.T) {
	server := NewServer(&fakeApp{err: saga.ErrNotFound})

	_, err := server.GetPaymentSaga(context.Background(), &sagav1.GetPaymentSagaRequest{PaymentId: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s, want %s: %v", status.Code(err), codes.NotFound, err)
	}
}

func TestStartPaymentSagaRejectsInvalidRequest(t *testing.T) {
	server := NewServer(&fakeApp{})

	_, err := server.StartPaymentSaga(context.Background(), &sagav1.StartPaymentSagaRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, want %s: %v", status.Code(err), codes.InvalidArgument, err)
	}
}

func validStartRequest() *sagav1.StartPaymentSagaRequest {
	return &sagav1.StartPaymentSagaRequest{
		PaymentId:      "payment-1",
		IdempotencyKey: "idem-1",
		UserId:         "user-1",
		FromWalletId:   "from-1",
		ToWalletId:     "to-1",
		AmountCents:    1250,
		Currency:       "USD",
	}
}

type fakeApp struct {
	startedReq saga.StartRequest
	started    saga.Saga
	got        saga.Saga
	err        error
	decision   saga.FraudReviewDecision
	decided    string
	reviewed   saga.Saga
	reviewErr  error
}

func (app *fakeApp) ResumeFraudReview(_ context.Context, decision saga.FraudReviewDecision) (saga.Saga, error) {
	app.decision = decision
	app.decided = "resume"
	return app.reviewed, app.reviewErr
}

func (app *fakeApp) RejectFraudReview(_ context.Context, decision saga.FraudReviewDecision) (saga.Saga, error) {
	app.decision = decision
	app.decided = "reject"
	return app.reviewed, app.reviewErr
}

func (app *fakeApp) StartPaymentSaga(_ context.Context, req saga.StartRequest) (saga.Saga, error) {
	app.startedReq = req
	if app.err != nil {
		return saga.Saga{}, app.err
	}
	return app.started, nil
}

func (app *fakeApp) GetPaymentSaga(context.Context, string) (saga.Saga, error) {
	if app.err != nil {
		return saga.Saga{}, app.err
	}
	return app.got, nil
}

func (app *fakeApp) ResumeNonTerminal(context.Context) error {
	return errors.New("unused")
}
