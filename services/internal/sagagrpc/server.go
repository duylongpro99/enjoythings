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
	ResumeFraudReview(context.Context, saga.FraudReviewDecision) (saga.Saga, error)
	RejectFraudReview(context.Context, saga.FraudReviewDecision) (saga.Saga, error)
	ListFraudReviews(context.Context) ([]saga.Saga, error)
	GetFraudReview(context.Context, string) (saga.FraudReview, error)
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

func (server *Server) ResumeFraudReview(ctx context.Context, req *sagav1.ResumeFraudReviewRequest) (*sagav1.FraudReviewResponse, error) {
	decision, err := reviewDecision(req.GetPaymentId(), req.GetActorId(), req.GetReason(), req.GetTraceId())
	if err != nil {
		return nil, err
	}
	current, err := server.app.ResumeFraudReview(ctx, decision)
	if err != nil {
		return nil, statusFromSaga(err)
	}
	return &sagav1.FraudReviewResponse{Saga: sagaMessage(current)}, nil
}

func (server *Server) RejectFraudReview(ctx context.Context, req *sagav1.RejectFraudReviewRequest) (*sagav1.FraudReviewResponse, error) {
	decision, err := reviewDecision(req.GetPaymentId(), req.GetActorId(), req.GetReason(), req.GetTraceId())
	if err != nil {
		return nil, err
	}
	current, err := server.app.RejectFraudReview(ctx, decision)
	if err != nil {
		return nil, statusFromSaga(err)
	}
	return &sagav1.FraudReviewResponse{Saga: sagaMessage(current)}, nil
}

func (server *Server) ListFraudReviews(ctx context.Context, _ *sagav1.ListFraudReviewsRequest) (*sagav1.ListFraudReviewsResponse, error) {
	sagas, err := server.app.ListFraudReviews(ctx)
	if err != nil {
		return nil, statusFromSaga(err)
	}
	resp := &sagav1.ListFraudReviewsResponse{Sagas: make([]*sagav1.PaymentSaga, 0, len(sagas))}
	for _, current := range sagas {
		resp.Sagas = append(resp.Sagas, sagaMessage(current))
	}
	return resp, nil
}

func (server *Server) GetFraudReview(ctx context.Context, req *sagav1.GetFraudReviewRequest) (*sagav1.GetFraudReviewResponse, error) {
	if req == nil || req.GetPaymentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id is required")
	}
	review, err := server.app.GetFraudReview(ctx, req.GetPaymentId())
	if err != nil {
		return nil, statusFromSaga(err)
	}
	resp := &sagav1.GetFraudReviewResponse{
		Saga:  sagaMessage(review.Saga),
		Audit: make([]*sagav1.FraudAuditRecord, 0, len(review.Audit)),
	}
	for _, record := range review.Audit {
		resp.Audit = append(resp.Audit, auditMessage(record))
	}
	return resp, nil
}

// reviewDecision requires an actor: a review exit is a human decision, and an
// audit record without one cannot be traced back to anybody.
func reviewDecision(paymentID, actorID, reason, traceID string) (saga.FraudReviewDecision, error) {
	if paymentID == "" || actorID == "" {
		return saga.FraudReviewDecision{}, status.Error(codes.InvalidArgument, "payment_id and actor_id are required")
	}
	return saga.FraudReviewDecision{
		PaymentID: paymentID,
		ActorID:   actorID,
		Reason:    reason,
		TraceID:   traceID,
	}, nil
}

func statusFromSaga(err error) error {
	switch {
	case errors.Is(err, saga.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, "idempotency key conflicts with another payload")
	case errors.Is(err, saga.ErrNotFound):
		return status.Error(codes.NotFound, "payment saga not found")
	case errors.Is(err, saga.ErrUnverified):
		return status.Error(codes.FailedPrecondition, "user is not verified")
	case errors.Is(err, saga.ErrNotUnderReview):
		return status.Error(codes.FailedPrecondition, "payment is not under fraud review")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

func sagaMessage(current saga.Saga) *sagav1.PaymentSaga {
	message := &sagav1.PaymentSaga{
		PaymentId:           current.PaymentID,
		Status:              current.State,
		FromWalletId:        current.FromWalletID,
		ToWalletId:          current.ToWalletID,
		AmountCents:         current.AmountCents,
		Currency:            current.Currency,
		FailureCode:         current.FailureCode,
		FailureMessage:      current.LastError,
		CreatedAt:           timestamppb.New(current.CreatedAt),
		UpdatedAt:           timestamppb.New(current.UpdatedAt),
		UserId:              current.UserID,
		FraudSessionId:      current.FraudSessionID,
		FraudAction:         current.FraudAction,
		FraudRiskScore:      current.FraudRiskScore,
		FraudReason:         current.FraudReason,
		DeferredPaymentJson: current.DeferredPaymentJSON,
	}
	// Unlike created_at, a flagged-at time is absent until the fraud worker
	// flags the payment; a zero time on the wire would read as year one.
	if !current.FraudFlaggedAt.IsZero() {
		message.FraudFlaggedAt = timestamppb.New(current.FraudFlaggedAt)
	}
	return message
}

func auditMessage(record saga.FraudAuditRecord) *sagav1.FraudAuditRecord {
	return &sagav1.FraudAuditRecord{
		EventId:     record.EventID,
		PaymentId:   record.PaymentID,
		Kind:        record.Kind,
		SagaState:   record.SagaState,
		DetailsJson: record.DetailsJSON,
		CreatedAt:   timestamppb.New(record.CreatedAt),
	}
}
