package client

import (
	"context"
	"testing"
	"time"

	walletv1 "enjoythings/services/gen/wallet/v1"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWalletClientMapsCreateWalletResponse(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC)
	grpcClient := &fakeWalletServiceClient{
		createResponse: &walletv1.CreateWalletResponse{Wallet: &walletv1.Wallet{
			Id:           walletID.String(),
			UserId:       userID.String(),
			BalanceCents: 500,
			Currency:     "USD",
			CreatedAt:    timestamppb.New(createdAt),
			UpdatedAt:    timestamppb.New(createdAt.Add(time.Minute)),
		}},
	}
	client := NewWalletClient(grpcClient)

	wallet, err := client.CreateWallet(context.Background(), userID, "usd")
	if err != nil {
		t.Fatalf("CreateWallet error = %v", err)
	}

	if grpcClient.createRequest.GetUserId() != userID.String() || grpcClient.createRequest.GetCurrency() != "usd" {
		t.Fatalf("create request = %+v", grpcClient.createRequest)
	}
	if wallet.ID != walletID || wallet.UserID != userID || wallet.Balance != 500 || wallet.Currency != "USD" || !wallet.CreatedAt.Equal(createdAt) {
		t.Fatalf("wallet = %+v", wallet)
	}
}

func TestWalletClientSendsUserMetadataForAuthenticatedRPCs(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	grpcClient := &fakeWalletServiceClient{
		balanceResponse: &walletv1.GetBalanceResponse{WalletId: walletID.String(), BalanceCents: 750, Currency: "USD"},
	}
	client := NewWalletClient(grpcClient)

	wallet, err := client.GetBalance(context.Background(), userID, walletID)
	if err != nil {
		t.Fatalf("GetBalance error = %v", err)
	}

	if got := grpcClient.outgoingUserID; got != userID.String() {
		t.Fatalf("x-user-id metadata = %q, want %q", got, userID.String())
	}
	if wallet.ID != walletID || wallet.UserID != userID || wallet.Balance != 750 || wallet.Currency != "USD" {
		t.Fatalf("wallet = %+v", wallet)
	}
}

func TestWalletClientMapsTransferResponse(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	transferID := uuid.New()
	grpcClient := &fakeWalletServiceClient{
		transferResponse: &walletv1.InitiateTransferResponse{
			TransferId: transferID.String(),
			Status:     "completed",
			Balances:   &walletv1.BalanceChange{FromBalanceCents: 250, ToBalanceCents: 1250},
		},
	}
	client := NewWalletClient(grpcClient)

	transfer, err := client.CreateTransfer(context.Background(), userID, fromID, toID, 1000)
	if err != nil {
		t.Fatalf("CreateTransfer error = %v", err)
	}

	if grpcClient.transferRequest.GetFromWalletId() != fromID.String() || grpcClient.transferRequest.GetToWalletId() != toID.String() || grpcClient.transferRequest.GetAmountCents() != 1000 {
		t.Fatalf("transfer request = %+v", grpcClient.transferRequest)
	}
	if grpcClient.outgoingUserID != userID.String() {
		t.Fatalf("x-user-id metadata = %q, want %q", grpcClient.outgoingUserID, userID.String())
	}
	if transfer.ID != transferID || transfer.FromWalletID != fromID || transfer.ToWalletID != toID || transfer.Amount != 1000 || transfer.Status != "completed" || transfer.FromBalance != 250 || transfer.ToBalance != 1250 {
		t.Fatalf("transfer = %+v", transfer)
	}
}

type fakeWalletServiceClient struct {
	createRequest    *walletv1.CreateWalletRequest
	createResponse   *walletv1.CreateWalletResponse
	balanceRequest   *walletv1.GetBalanceRequest
	balanceResponse  *walletv1.GetBalanceResponse
	transferRequest  *walletv1.InitiateTransferRequest
	transferResponse *walletv1.InitiateTransferResponse
	outgoingUserID   string
	err              error
}

func (client *fakeWalletServiceClient) CreateWallet(_ context.Context, req *walletv1.CreateWalletRequest, _ ...grpc.CallOption) (*walletv1.CreateWalletResponse, error) {
	client.createRequest = req
	return client.createResponse, client.err
}

func (client *fakeWalletServiceClient) GetWallet(ctx context.Context, req *walletv1.GetWalletRequest, _ ...grpc.CallOption) (*walletv1.GetWalletResponse, error) {
	client.captureOutgoingUserID(ctx)
	return &walletv1.GetWalletResponse{Wallet: &walletv1.Wallet{Id: req.GetWalletId(), UserId: client.outgoingUserID}}, client.err
}

func (client *fakeWalletServiceClient) GetBalance(ctx context.Context, req *walletv1.GetBalanceRequest, _ ...grpc.CallOption) (*walletv1.GetBalanceResponse, error) {
	client.balanceRequest = req
	client.captureOutgoingUserID(ctx)
	return client.balanceResponse, client.err
}

func (client *fakeWalletServiceClient) InitiateTransfer(ctx context.Context, req *walletv1.InitiateTransferRequest, _ ...grpc.CallOption) (*walletv1.InitiateTransferResponse, error) {
	client.transferRequest = req
	client.captureOutgoingUserID(ctx)
	return client.transferResponse, client.err
}

func (client *fakeWalletServiceClient) DebitForSaga(context.Context, *walletv1.DebitForSagaRequest, ...grpc.CallOption) (*walletv1.DebitForSagaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DebitForSaga is not used by these tests")
}

func (client *fakeWalletServiceClient) CompensateDebit(context.Context, *walletv1.CompensateDebitRequest, ...grpc.CallOption) (*walletv1.CompensateDebitResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CompensateDebit is not used by these tests")
}

func (client *fakeWalletServiceClient) captureOutgoingUserID(ctx context.Context) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return
	}
	values := md.Get("x-user-id")
	if len(values) > 0 {
		client.outgoingUserID = values[0]
	}
}
