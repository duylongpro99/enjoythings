package client

import (
	"context"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"
)

type SagaClient struct {
	client sagav1.SagaServiceClient
}

func NewSagaClient(client sagav1.SagaServiceClient) *SagaClient {
	return &SagaClient{client: client}
}

func (client *SagaClient) StartPayment(ctx context.Context, req saga.StartRequest) (saga.Saga, error) {
	resp, err := client.client.StartPaymentSaga(ctx, &sagav1.StartPaymentSagaRequest{
		PaymentId:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceId:        req.TraceID,
		UserId:         req.UserID,
		FromWalletId:   req.FromWalletID,
		ToWalletId:     req.ToWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
	})
	if err != nil {
		return saga.Saga{}, err
	}
	current := saga.Saga{
		PaymentID: resp.GetPaymentId(),
		State:     resp.GetStatus(),
	}
	if resp.GetAcceptedAt() != nil {
		current.CreatedAt = resp.GetAcceptedAt().AsTime()
	}
	return current, nil
}

func (client *SagaClient) GetPayment(ctx context.Context, paymentID, traceID string) (saga.Saga, error) {
	resp, err := client.client.GetPaymentSaga(ctx, &sagav1.GetPaymentSagaRequest{
		PaymentId: paymentID,
		TraceId:   traceID,
	})
	if err != nil {
		return saga.Saga{}, err
	}
	message := resp.GetSaga()
	current := saga.Saga{
		PaymentID:    message.GetPaymentId(),
		State:        message.GetStatus(),
		FromWalletID: message.GetFromWalletId(),
		ToWalletID:   message.GetToWalletId(),
		AmountCents:  message.GetAmountCents(),
		Currency:     message.GetCurrency(),
		FailureCode:  message.GetFailureCode(),
		LastError:    message.GetFailureMessage(),
	}
	if message.GetCreatedAt() != nil {
		current.CreatedAt = message.GetCreatedAt().AsTime()
	}
	if message.GetUpdatedAt() != nil {
		current.UpdatedAt = message.GetUpdatedAt().AsTime()
	}
	return current, nil
}
