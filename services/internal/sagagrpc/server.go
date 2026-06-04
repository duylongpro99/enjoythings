package sagagrpc

import (
	"context"
	"errors"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App interface {
	StartPaymentSaga(context.Context, saga.StartRequest) (saga.Saga, error)
	GetPaymentSaga(context.Context, string) (saga.Saga, error)
	ResumeNonTerminal(context.Context) error
}

type Server struct {
	sagav1.UnimplementedSagaServiceServer
	app App
}

func NewServer(app App) *Server {
	return &Server{app: app}
}

func (server *Server) StartPaymentSaga(ctx context.Context, req *sagav1.StartPaymentSagaRequest) (*sagav1.StartPaymentSagaResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.GetPaymentId() == "" || req.GetIdempotencyKey() == "" || req.GetUserId() == "" ||
		req.GetFromWalletId() == "" || req.GetToWalletId() == "" || req.GetCurrency() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id, idempotency_key, user_id, wallet IDs, and currency are required")
	}
	if req.GetAmountCents() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_cents must be positive")
	}
	current, err := server.app.StartPaymentSaga(ctx, saga.StartRequest{
		PaymentID:      req.GetPaymentId(),
		IdempotencyKey: req.GetIdempotencyKey(),
		TraceID:        req.GetTraceId(),
		UserID:         req.GetUserId(),
		FromWalletID:   req.GetFromWalletId(),
		ToWalletID:     req.GetToWalletId(),
		AmountCents:    req.GetAmountCents(),
		Currency:       req.GetCurrency(),
	})
	if err != nil {
		return nil, statusFromSaga(err)
	}
	return &sagav1.StartPaymentSagaResponse{
		PaymentId:  current.PaymentID,
		Status:     current.State,
		AcceptedAt: timestamppb.New(current.CreatedAt),
	}, nil
}

func (server *Server) GetPaymentSaga(ctx context.Context, req *sagav1.GetPaymentSagaRequest) (*sagav1.GetPaymentSagaResponse, error) {
	if req == nil || req.GetPaymentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id is required")
	}
	current, err := server.app.GetPaymentSaga(ctx, req.GetPaymentId())
	if err != nil {
		return nil, statusFromSaga(err)
	}
	return &sagav1.GetPaymentSagaResponse{Saga: sagaMessage(current)}, nil
}

func statusFromSaga(err error) error {
	switch {
	case errors.Is(err, saga.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, "idempotency key conflicts with another payload")
	case errors.Is(err, saga.ErrNotFound):
		return status.Error(codes.NotFound, "payment saga not found")
	case errors.Is(err, saga.ErrUnverified):
		return status.Error(codes.FailedPrecondition, "user is not verified")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

func sagaMessage(current saga.Saga) *sagav1.PaymentSaga {
	return &sagav1.PaymentSaga{
		PaymentId:      current.PaymentID,
		Status:         current.State,
		FromWalletId:   current.FromWalletID,
		ToWalletId:     current.ToWalletID,
		AmountCents:    current.AmountCents,
		Currency:       current.Currency,
		FailureMessage: current.LastError,
		CreatedAt:      timestamppb.New(current.CreatedAt),
		UpdatedAt:      timestamppb.New(current.UpdatedAt),
	}
}
