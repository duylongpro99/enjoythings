package sagagrpc

import (
	"context"
	"testing"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResumeFraudReviewMapsRequestAndResponse(t *testing.T) {
	app := &fakeApp{reviewed: saga.Saga{
		PaymentID: "payment-1",
		State:     saga.StateCompleted,
		UpdatedAt: fixedTime,
	}}
	server := NewServer(app)

	resp, err := server.ResumeFraudReview(context.Background(), &sagav1.ResumeFraudReviewRequest{
		PaymentId: "payment-1",
		ActorId:   "operator-1",
		Reason:    "cleared",
		TraceId:   "trace-1",
	})
	if err != nil {
		t.Fatalf("ResumeFraudReview: %v", err)
	}
	if app.decided != "resume" {
		t.Fatalf("decision = %q, want resume", app.decided)
	}
	want := saga.FraudReviewDecision{PaymentID: "payment-1", ActorID: "operator-1", Reason: "cleared", TraceID: "trace-1"}
	if app.decision != want {
		t.Fatalf("decision = %+v, want %+v", app.decision, want)
	}
	if resp.GetSaga().GetStatus() != saga.StateCompleted {
		t.Fatalf("status = %q, want %q", resp.GetSaga().GetStatus(), saga.StateCompleted)
	}
}

func TestRejectFraudReviewMapsRequestAndResponse(t *testing.T) {
	app := &fakeApp{reviewed: saga.Saga{
		PaymentID:   "payment-1",
		State:       saga.StateFailed,
		FailureCode: saga.FailureCodeFraudRejected,
		LastError:   "confirmed fraud",
	}}
	server := NewServer(app)

	resp, err := server.RejectFraudReview(context.Background(), &sagav1.RejectFraudReviewRequest{
		PaymentId: "payment-1",
		ActorId:   "operator-2",
		Reason:    "confirmed fraud",
	})
	if err != nil {
		t.Fatalf("RejectFraudReview: %v", err)
	}
	if app.decided != "reject" {
		t.Fatalf("decision = %q, want reject", app.decided)
	}
	if resp.GetSaga().GetFailureCode() != saga.FailureCodeFraudRejected {
		t.Fatalf("failure code = %q, want %q", resp.GetSaga().GetFailureCode(), saga.FailureCodeFraudRejected)
	}
}

func TestFraudReviewRequiresPaymentAndActor(t *testing.T) {
	server := NewServer(&fakeApp{})

	if _, err := server.ResumeFraudReview(context.Background(), &sagav1.ResumeFraudReviewRequest{ActorId: "operator-1"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing payment id = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if _, err := server.RejectFraudReview(context.Background(), &sagav1.RejectFraudReviewRequest{PaymentId: "payment-1"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing actor id = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestFraudReviewMapsNotUnderReviewToFailedPrecondition(t *testing.T) {
	server := NewServer(&fakeApp{reviewErr: saga.ErrNotUnderReview})

	_, err := server.ResumeFraudReview(context.Background(), &sagav1.ResumeFraudReviewRequest{
		PaymentId: "payment-1",
		ActorId:   "operator-1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.FailedPrecondition)
	}
}

func TestFraudReviewMapsMissingSagaToNotFound(t *testing.T) {
	server := NewServer(&fakeApp{reviewErr: saga.ErrNotFound})

	_, err := server.RejectFraudReview(context.Background(), &sagav1.RejectFraudReviewRequest{
		PaymentId: "payment-1",
		ActorId:   "operator-1",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.NotFound)
	}
}
