package client

import (
	"context"
	"fmt"

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/domain"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
)

const userIDMetadataKey = "x-user-id"

type WalletClient struct {
	client walletv1.WalletServiceClient
}

func NewWalletClient(client walletv1.WalletServiceClient) *WalletClient {
	return &WalletClient{client: client}
}

func (client *WalletClient) CreateWallet(ctx context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	resp, err := client.client.CreateWallet(ctx, &walletv1.CreateWalletRequest{
		UserId:   userID.String(),
		Currency: currency,
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return walletFromMessage(resp.GetWallet())
}

func (client *WalletClient) GetWallet(ctx context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	resp, err := client.client.GetWallet(contextWithUserID(ctx, userID), &walletv1.GetWalletRequest{
		WalletId: walletID.String(),
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return walletFromMessage(resp.GetWallet())
}

func (client *WalletClient) GetBalance(ctx context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	resp, err := client.client.GetBalance(contextWithUserID(ctx, userID), &walletv1.GetBalanceRequest{
		WalletId: walletID.String(),
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return domain.Wallet{
		ID:       walletID,
		UserID:   userID,
		Balance:  resp.GetBalanceCents(),
		Currency: resp.GetCurrency(),
	}, nil
}

func (client *WalletClient) CreateTransfer(ctx context.Context, userID, fromWalletID, toWalletID uuid.UUID, amount int64) (domain.Transfer, error) {
	resp, err := client.client.InitiateTransfer(contextWithUserID(ctx, userID), &walletv1.InitiateTransferRequest{
		FromWalletId: fromWalletID.String(),
		ToWalletId:   toWalletID.String(),
		AmountCents:  amount,
	})
	if err != nil {
		return domain.Transfer{}, err
	}
	transferID, err := uuid.Parse(resp.GetTransferId())
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("wallet grpc transfer_id is invalid: %w", err)
	}
	balances := resp.GetBalances()
	return domain.Transfer{
		ID:           transferID,
		FromWalletID: fromWalletID,
		ToWalletID:   toWalletID,
		Amount:       amount,
		Status:       resp.GetStatus(),
		FromBalance:  balances.GetFromBalanceCents(),
		ToBalance:    balances.GetToBalanceCents(),
	}, nil
}

func contextWithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return metadata.AppendToOutgoingContext(ctx, userIDMetadataKey, userID.String())
}

func walletFromMessage(message *walletv1.Wallet) (domain.Wallet, error) {
	if message == nil {
		return domain.Wallet{}, fmt.Errorf("wallet grpc response missing wallet")
	}
	walletID, err := uuid.Parse(message.GetId())
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("wallet grpc id is invalid: %w", err)
	}
	userID, err := uuid.Parse(message.GetUserId())
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("wallet grpc user_id is invalid: %w", err)
	}
	wallet := domain.Wallet{
		ID:       walletID,
		UserID:   userID,
		Balance:  message.GetBalanceCents(),
		Currency: message.GetCurrency(),
	}
	if message.GetCreatedAt() != nil {
		wallet.CreatedAt = message.GetCreatedAt().AsTime()
	}
	if message.GetUpdatedAt() != nil {
		wallet.UpdatedAt = message.GetUpdatedAt().AsTime()
	}
	return wallet, nil
}
