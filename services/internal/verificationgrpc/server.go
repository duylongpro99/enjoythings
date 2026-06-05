package verificationgrpc

import (
	"context"
	"errors"
	"time"

	verificationv1 "enjoythings/services/gen/verification/v1"
	"enjoythings/services/internal/verification"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App interface {
	Submit(context.Context, verification.SubmitCommand) (verification.Record, error)
	GetStatus(context.Context, string) (verification.Record, error)
}

type Server struct {
	verificationv1.UnimplementedVerificationServiceServer
	app App
}

func NewServer(app App) *Server {
	return &Server{app: app}
}

func (server *Server) SubmitVerification(ctx context.Context, req *verificationv1.SubmitVerificationRequest) (*verificationv1.SubmitVerificationResponse, error) {
	if req == nil || req.GetUserId() == "" || req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and idempotency_key are required")
	}
	record, err := server.app.Submit(ctx, verification.SubmitCommand{
		PaymentID:      req.GetPaymentId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		TraceID:        req.GetTraceId(),
		UserID:         req.GetUserId(),
		VerificationID: req.GetVerificationId(),
		Decision:       req.GetDecision(),
		Reason:         req.GetReason(),
	})
	if err != nil {
		return nil, statusFromVerification(err)
	}
	return &verificationv1.SubmitVerificationResponse{
		VerificationId: record.VerificationID,
		UserId:         record.UserID,
		Status:         record.Status,
		DecidedAt:      timestampOrNil(record.DecidedAt),
	}, nil
}

func (server *Server) GetStatus(ctx context.Context, req *verificationv1.GetStatusRequest) (*verificationv1.GetStatusResponse, error) {
	if req == nil || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	record, err := server.app.GetStatus(ctx, req.GetUserId())
	if err != nil {
		return nil, statusFromVerification(err)
	}
	return &verificationv1.GetStatusResponse{
		UserId:         record.UserID,
		Status:         record.Status,
		VerificationId: record.VerificationID,
		Reason:         record.Reason,
		UpdatedAt:      timestampOrNil(record.UpdatedAt),
	}, nil
}

func statusFromVerification(err error) error {
	switch {
	case errors.Is(err, verification.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, "verification request is invalid")
	case errors.Is(err, verification.ErrNotFound):
		return status.Error(codes.NotFound, "verification not found")
	case errors.Is(err, verification.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, "verification state transition is not allowed")
	case errors.Is(err, verification.ErrIdempotencyKeyConflict):
		return status.Error(codes.AlreadyExists, "idempotency key conflicts with another verification request")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

func timestampOrNil(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}
