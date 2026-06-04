package saga

import (
	"context"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	verificationv1 "enjoythings/services/gen/verification/v1"
	walletv1 "enjoythings/services/gen/wallet/v1"

	"google.golang.org/grpc"
)

type VerificationGRPCClient struct {
	client verificationv1.VerificationServiceClient
}

func NewVerificationGRPCClient(conn grpc.ClientConnInterface) *VerificationGRPCClient {
	return &VerificationGRPCClient{client: verificationv1.NewVerificationServiceClient(conn)}
}

func (client *VerificationGRPCClient) GetStatus(ctx context.Context, req VerificationRequest) (VerificationResult, error) {
	resp, err := client.client.GetStatus(ctx, &verificationv1.GetStatusRequest{
		UserId:  req.UserID,
		TraceId: req.TraceID,
	})
	if err != nil {
		return VerificationResult{}, err
	}
	return VerificationResult{Status: resp.GetStatus()}, nil
}

type WalletGRPCClient struct {
	client walletv1.WalletServiceClient
}

func NewWalletGRPCClient(conn grpc.ClientConnInterface) *WalletGRPCClient {
	return &WalletGRPCClient{client: walletv1.NewWalletServiceClient(conn)}
}

func (client *WalletGRPCClient) DebitForSaga(ctx context.Context, req WalletDebitRequest) (WalletDebitResult, error) {
	resp, err := client.client.DebitForSaga(ctx, &walletv1.DebitForSagaRequest{
		PaymentId:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceId:        req.TraceID,
		FromWalletId:   req.FromWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
	})
	if err != nil {
		return WalletDebitResult{}, err
	}
	return WalletDebitResult{WalletDebitID: resp.GetWalletDebitId()}, nil
}

func (client *WalletGRPCClient) CompensateDebit(ctx context.Context, req WalletCompensateRequest) error {
	_, err := client.client.CompensateDebit(ctx, &walletv1.CompensateDebitRequest{
		PaymentId:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceId:        req.TraceID,
		FromWalletId:   req.FromWalletID,
		WalletDebitId:  req.WalletDebitID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
		Reason:         req.Reason,
	})
	return err
}

type LedgerGRPCClient struct {
	client ledgerv1.LedgerServiceClient
}

func NewLedgerGRPCClient(conn grpc.ClientConnInterface) *LedgerGRPCClient {
	return &LedgerGRPCClient{client: ledgerv1.NewLedgerServiceClient(conn)}
}

func (client *LedgerGRPCClient) ReserveTransfer(ctx context.Context, req LedgerReserveRequest) (LedgerReserveResult, error) {
	resp, err := client.client.ReserveTransfer(ctx, &ledgerv1.ReserveTransferRequest{
		PaymentId:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceId:        req.TraceID,
		FromWalletId:   req.FromWalletID,
		ToWalletId:     req.ToWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
	})
	if err != nil {
		return LedgerReserveResult{}, err
	}
	return LedgerReserveResult{LedgerReservationID: resp.GetLedgerReservationId()}, nil
}

func (client *LedgerGRPCClient) ConfirmTransfer(ctx context.Context, req LedgerConfirmRequest) (LedgerConfirmResult, error) {
	resp, err := client.client.ConfirmTransfer(ctx, &ledgerv1.ConfirmTransferRequest{
		PaymentId:           req.PaymentID,
		IdempotencyKey:      req.IdempotencyKey,
		TraceId:             req.TraceID,
		LedgerReservationId: req.LedgerReservationID,
		WalletDebitId:       req.WalletDebitID,
	})
	if err != nil {
		return LedgerConfirmResult{}, err
	}
	return LedgerConfirmResult{TransferID: resp.GetTransferId(), CompletedAt: resp.GetCompletedAt().AsTime()}, nil
}

func (client *LedgerGRPCClient) CancelReservation(ctx context.Context, req LedgerCancelRequest) error {
	_, err := client.client.CancelReservation(ctx, &ledgerv1.CancelReservationRequest{
		PaymentId:           req.PaymentID,
		IdempotencyKey:      req.IdempotencyKey,
		TraceId:             req.TraceID,
		LedgerReservationId: req.LedgerReservationID,
		Reason:              req.Reason,
	})
	return err
}
