package verificationgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	verificationv1 "enjoythings/services/gen/verification/v1"
	"enjoythings/services/internal/verification"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubmitVerificationValidatesUserAndIdempotencyKey(t *testing.T) {
	server := NewServer(&fakeApp{})

	_, err := server.SubmitVerification(context.Background(), &verificationv1.SubmitVerificationRequest{
		UserId: "user-1",
	})

	assertCode(t, err, codes.InvalidArgument)
}

func TestSubmitVerificationMapsResponse(t *testing.T) {
	decidedAt := time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)
	app := &fakeApp{
		submitRecord: verification.Record{
			VerificationID: "ver-1",
			UserID:         "user-1",
			Status:         verification.StatusVerified,
			DecidedAt:      decidedAt,
		},
	}
	server := NewServer(app)

	resp, err := server.SubmitVerification(context.Background(), &verificationv1.SubmitVerificationRequest{
		UserId:         "user-1",
		IdempotencyKey: "key-1",
		TraceId:        "trace-1",
	})
	if err != nil {
		t.Fatalf("SubmitVerification error = %v", err)
	}

	if app.submitCommand.UserID != "user-1" || app.submitCommand.IdempotencyKey != "key-1" || app.submitCommand.TraceID != "trace-1" {
		t.Fatalf("submit command = %+v", app.submitCommand)
	}
	if resp.GetVerificationId() != "ver-1" || resp.GetUserId() != "user-1" || resp.GetStatus() != verification.StatusVerified {
		t.Fatalf("response = %+v", resp)
	}
	if !resp.GetDecidedAt().AsTime().Equal(decidedAt) {
		t.Fatalf("decided_at = %s, want %s", resp.GetDecidedAt().AsTime(), decidedAt)
	}
}

func TestGetStatusMapsErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		code codes.Code
	}{
		"not found":           {err: verification.ErrNotFound, code: codes.NotFound},
		"failed precondition": {err: verification.ErrFailedPrecondition, code: codes.FailedPrecondition},
		"unexpected":          {err: errors.New("database unavailable"), code: codes.Internal},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := NewServer(&fakeApp{statusErr: tc.err})

			_, err := server.GetStatus(context.Background(), &verificationv1.GetStatusRequest{UserId: "user-1"})

			assertCode(t, err, tc.code)
		})
	}
}

type fakeApp struct {
	submitCommand verification.SubmitCommand
	submitRecord  verification.Record
	submitErr     error
	statusRecord  verification.Record
	statusErr     error
}

func (app *fakeApp) Submit(ctx context.Context, cmd verification.SubmitCommand) (verification.Record, error) {
	app.submitCommand = cmd
	return app.submitRecord, app.submitErr
}

func (app *fakeApp) GetStatus(ctx context.Context, userID string) (verification.Record, error) {
	if app.statusRecord.UserID == "" {
		app.statusRecord.UserID = userID
	}
	return app.statusRecord, app.statusErr
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("code = %s, want %s; err=%v", status.Code(err), want, err)
	}
}
