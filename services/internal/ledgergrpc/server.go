package ledgergrpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const userIDMetadataKey = "x-user-id"

type App interface {
	ListLedger(context.Context, uuid.UUID, uuid.UUID, repo.LedgerCursor, int) ([]domain.LedgerEntry, repo.LedgerCursor, error)
	GetFraudTransactionHistory(context.Context, uuid.UUID, int, string) ([]domain.FraudTransactionSummary, error)
	GetFraudVelocityMetrics(context.Context, uuid.UUID, time.Time, string) (domain.FraudVelocityMetrics, error)
	ReserveTransfer(context.Context, domain.LedgerReserveCommand) (domain.LedgerReservation, error)
	ConfirmTransfer(context.Context, domain.LedgerConfirmCommand) (domain.LedgerConfirmation, error)
	CancelReservation(context.Context, domain.LedgerCancelCommand) (domain.LedgerReservation, error)
}

type Server struct {
	ledgerv1.UnimplementedLedgerServiceServer
	app App
}

func NewServer(app App) *Server {
	return &Server{app: app}
}

func (server *Server) GetEntries(ctx context.Context, req *ledgerv1.GetEntriesRequest) (*ledgerv1.GetEntriesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	userID, err := userIDFromMetadata(ctx)
	if err != nil {
		return nil, err
	}
	walletID, err := parseUUID(req.GetWalletId(), "wallet_id")
	if err != nil {
		return nil, err
	}
	if req.GetLimit() < 0 || req.GetLimit() > 100 {
		return nil, status.Error(codes.InvalidArgument, "limit is invalid")
	}
	cursor, err := DecodeCursor(req.GetCursor())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "cursor is invalid")
	}

	entries, next, err := server.app.ListLedger(ctx, userID, walletID, cursor, int(req.GetLimit()))
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.GetEntriesResponse{
		WalletId:   walletID.String(),
		Entries:    entryMessages(entries),
		NextCursor: EncodeCursor(next),
	}, nil
}

func (server *Server) GetFraudTransactionHistory(ctx context.Context, req *ledgerv1.GetFraudTransactionHistoryRequest) (*ledgerv1.GetFraudTransactionHistoryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	walletID, err := parseUUID(req.GetWalletId(), "wallet_id")
	if err != nil {
		return nil, err
	}
	if req.GetLimit() < 1 || req.GetLimit() > 100 {
		return nil, status.Error(codes.InvalidArgument, "limit is invalid")
	}
	entries, err := server.app.GetFraudTransactionHistory(ctx, walletID, int(req.GetLimit()), req.GetTraceId())
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.GetFraudTransactionHistoryResponse{Entries: fraudHistoryMessages(entries)}, nil
}

func (server *Server) GetFraudVelocityMetrics(ctx context.Context, req *ledgerv1.GetFraudVelocityMetricsRequest) (*ledgerv1.GetFraudVelocityMetricsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	walletID, err := parseUUID(req.GetWalletId(), "wallet_id")
	if err != nil {
		return nil, err
	}
	asOf := time.Now().UTC()
	metrics, err := server.app.GetFraudVelocityMetrics(ctx, walletID, asOf, req.GetTraceId())
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.GetFraudVelocityMetricsResponse{
		TransactionsLastHour:   metrics.TransactionsLastHour,
		AmountLastHourCents:    metrics.AmountLastHourCents,
		AverageAmount_30DCents: metrics.AverageAmount30dCents,
		DistinctRecipients_30D: metrics.DistinctRecipients30d,
	}, nil
}

func (server *Server) ReserveTransfer(ctx context.Context, req *ledgerv1.ReserveTransferRequest) (*ledgerv1.ReserveTransferResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	paymentID, err := parseUUID(req.GetPaymentId(), "payment_id")
	if err != nil {
		return nil, err
	}
	fromWalletID, err := parseUUID(req.GetFromWalletId(), "from_wallet_id")
	if err != nil {
		return nil, err
	}
	toWalletID, err := parseUUID(req.GetToWalletId(), "to_wallet_id")
	if err != nil {
		return nil, err
	}
	if req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	if req.GetAmountCents() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_cents must be positive")
	}
	if req.GetCurrency() == "" {
		return nil, status.Error(codes.InvalidArgument, "currency is required")
	}
	if fromWalletID == toWalletID {
		return nil, status.Error(codes.InvalidArgument, "from_wallet_id and to_wallet_id must differ")
	}

	reservation, err := server.app.ReserveTransfer(ctx, domain.LedgerReserveCommand{
		PaymentID:      paymentID,
		IdempotencyKey: req.GetIdempotencyKey(),
		TraceID:        req.GetTraceId(),
		FromWalletID:   fromWalletID,
		ToWalletID:     toWalletID,
		AmountCents:    req.GetAmountCents(),
		Currency:       req.GetCurrency(),
	})
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.ReserveTransferResponse{
		PaymentId:           reservation.PaymentID.String(),
		LedgerReservationId: reservation.ID.String(),
		Status:              reservation.Status,
	}, nil
}

func (server *Server) ConfirmTransfer(ctx context.Context, req *ledgerv1.ConfirmTransferRequest) (*ledgerv1.ConfirmTransferResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	paymentID, err := parseUUID(req.GetPaymentId(), "payment_id")
	if err != nil {
		return nil, err
	}
	reservationID, err := parseUUID(req.GetLedgerReservationId(), "ledger_reservation_id")
	if err != nil {
		return nil, err
	}
	walletDebitID, err := parseUUID(req.GetWalletDebitId(), "wallet_debit_id")
	if err != nil {
		return nil, err
	}
	if req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	confirmation, err := server.app.ConfirmTransfer(ctx, domain.LedgerConfirmCommand{
		PaymentID:           paymentID,
		IdempotencyKey:      req.GetIdempotencyKey(),
		TraceID:             req.GetTraceId(),
		LedgerReservationID: reservationID,
		WalletDebitID:       walletDebitID,
	})
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.ConfirmTransferResponse{
		PaymentId:   confirmation.PaymentID.String(),
		TransferId:  confirmation.TransferID.String(),
		Status:      confirmation.Status,
		CompletedAt: timestamppb.New(confirmation.CompletedAt),
	}, nil
}

func (server *Server) CancelReservation(ctx context.Context, req *ledgerv1.CancelReservationRequest) (*ledgerv1.CancelReservationResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	paymentID, err := parseUUID(req.GetPaymentId(), "payment_id")
	if err != nil {
		return nil, err
	}
	reservationID, err := parseUUID(req.GetLedgerReservationId(), "ledger_reservation_id")
	if err != nil {
		return nil, err
	}
	if req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	reservation, err := server.app.CancelReservation(ctx, domain.LedgerCancelCommand{
		PaymentID:           paymentID,
		IdempotencyKey:      req.GetIdempotencyKey(),
		TraceID:             req.GetTraceId(),
		LedgerReservationID: reservationID,
		Reason:              req.GetReason(),
	})
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &ledgerv1.CancelReservationResponse{
		PaymentId:           reservation.PaymentID.String(),
		LedgerReservationId: reservation.ID.String(),
		Status:              reservation.Status,
	}, nil
}

func userIDFromMetadata(ctx context.Context) (uuid.UUID, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return uuid.Nil, status.Error(codes.InvalidArgument, "x-user-id metadata is required")
	}
	values := md.Get(userIDMetadataKey)
	if len(values) == 0 || values[0] == "" {
		return uuid.Nil, status.Error(codes.InvalidArgument, "x-user-id metadata is required")
	}
	return parseUUID(values[0], userIDMetadataKey)
}

func parseUUID(raw string, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "%s must be a valid UUID", field)
	}
	return id, nil
}

func statusFromDomain(err error) error {
	switch {
	case errors.Is(err, domain.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, "reservation conflicts with existing payload")
	case errors.Is(err, domain.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, "reservation state does not allow this operation")
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "wallet not found")
	case errors.Is(err, domain.ErrInvalidAmount):
		return status.Error(codes.InvalidArgument, "amount or pagination is invalid")
	case errors.Is(err, domain.ErrInvalidTransfer):
		return status.Error(codes.InvalidArgument, "transfer is invalid")
	case errors.Is(err, domain.ErrUnsupportedCurrency):
		return status.Error(codes.InvalidArgument, "currency is unsupported")
	case errors.Is(err, domain.ErrCurrencyMismatch):
		return status.Error(codes.InvalidArgument, "currency mismatch")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

func entryMessages(entries []domain.LedgerEntry) []*ledgerv1.LedgerEntry {
	messages := make([]*ledgerv1.LedgerEntry, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, &ledgerv1.LedgerEntry{
			Id:                entry.ID.String(),
			TransferId:        entry.TransferID.String(),
			Direction:         entry.Direction,
			AmountCents:       entry.Amount,
			BalanceAfterCents: entry.BalanceAfter,
			CreatedAt:         timestamppb.New(entry.CreatedAt),
		})
	}
	return messages
}

func fraudHistoryMessages(entries []domain.FraudTransactionSummary) []*ledgerv1.FraudTransactionHistoryEntry {
	messages := make([]*ledgerv1.FraudTransactionHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, &ledgerv1.FraudTransactionHistoryEntry{
			Direction:   entry.Direction,
			AmountCents: entry.AmountCents,
			Currency:    entry.Currency,
			OccurredAt:  timestamppb.New(entry.OccurredAt),
		})
	}
	return messages
}

func EncodeCursor(cursor repo.LedgerCursor) string {
	if !cursor.Valid {
		return ""
	}
	payload, _ := json.Marshal(map[string]string{
		"created_at": cursor.CreatedAt.Format(time.RFC3339Nano),
		"id":         cursor.ID.String(),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(raw string) (repo.LedgerCursor, error) {
	if raw == "" {
		return repo.LedgerCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	var decoded struct {
		CreatedAt string `json:"created_at"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return repo.LedgerCursor{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, decoded.CreatedAt)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	id, err := uuid.Parse(decoded.ID)
	if err != nil {
		return repo.LedgerCursor{}, err
	}
	return repo.LedgerCursor{CreatedAt: createdAt, ID: id, Valid: true}, nil
}
