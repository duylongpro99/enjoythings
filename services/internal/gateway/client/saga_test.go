package client

import (
	"context"
	"testing"
	"time"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSagaClientMapsStartAndGetPayment(t *testing.T) {
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	grpcClient := &fakeSagaServiceClient{
		startResponse: &sagav1.StartPaymentSagaResponse{
			PaymentId:  "payment-1",
			Status:     saga.StatePaymentProcessing,
			AcceptedAt: timestamppb.New(now),
		},
		getResponse: &sagav1.GetPaymentSagaResponse{Saga: &sagav1.PaymentSaga{
			PaymentId:    "payment-1",
			Status:       saga.StateCompleted,
			FromWalletId: "from-1",
			ToWalletId:   "to-1",
			AmountCents:  1250,
			Currency:     "USD",
			CreatedAt:    timestamppb.New(now),
			UpdatedAt:    timestamppb.New(now.Add(time.Minute)),
		}},
	}
	client := NewSagaClient(grpcClient)

	started, err := client.StartPayment(context.Background(), saga.StartRequest{
		PaymentID:      "payment-1",
		IdempotencyKey: "idem-1",
		TraceID:        "trace-1",
		UserID:         "user-1",
		FromWalletID:   "from-1",
		ToWalletID:     "to-1",
		AmountCents:    1250,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("StartPayment: %v", err)
	}
	if grpcClient.startRequest.GetUserId() != "user-1" || grpcClient.startRequest.GetIdempotencyKey() != "idem-1" || grpcClient.startRequest.GetTraceId() != "trace-1" {
		t.Fatalf("start request = %+v", grpcClient.startRequest)
	}
	if started.PaymentID != "payment-1" || started.State != saga.StatePaymentProcessing || !started.CreatedAt.Equal(now) {
		t.Fatalf("started saga = %+v", started)
	}

	got, err := client.GetPayment(context.Background(), "payment-1", "trace-2")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if grpcClient.getRequest.GetPaymentId() != "payment-1" || grpcClient.getRequest.GetTraceId() != "trace-2" {
		t.Fatalf("get request = %+v", grpcClient.getRequest)
	}
	if got.State != saga.StateCompleted || got.FromWalletID != "from-1" || got.AmountCents != 1250 {
		t.Fatalf("got saga = %+v", got)
	}
}

type fakeSagaServiceClient struct {
	startRequest  *sagav1.StartPaymentSagaRequest
	startResponse *sagav1.StartPaymentSagaResponse
	getRequest    *sagav1.GetPaymentSagaRequest
	getResponse   *sagav1.GetPaymentSagaResponse
	err           error
}

func (client *fakeSagaServiceClient) StartPaymentSaga(_ context.Context, req *sagav1.StartPaymentSagaRequest, _ ...grpc.CallOption) (*sagav1.StartPaymentSagaResponse, error) {
	client.startRequest = req
	return client.startResponse, client.err
}

func (client *fakeSagaServiceClient) GetPaymentSaga(_ context.Context, req *sagav1.GetPaymentSagaRequest, _ ...grpc.CallOption) (*sagav1.GetPaymentSagaResponse, error) {
	client.getRequest = req
	return client.getResponse, client.err
}
