package walletgrpc

import (
	"context"
	"errors"

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/domain"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const userIDMetadataKey = "x-user-id"

type App interface {
	CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error)
	GetWallet(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	GetBalance(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error)
	CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error)
}

type Server struct {
	walletv1.UnimplementedWalletServiceServer
	app App
}

func NewServer(app App) *Server {
	return &Server{app: app}
}

func (server *Server) CreateWallet(ctx context.Context, req *walletv1.CreateWalletRequest) (*walletv1.CreateWalletResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	userID, err := parseUUID(req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	wallet, err := server.app.CreateWallet(ctx, userID, req.GetCurrency())
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &walletv1.CreateWalletResponse{Wallet: walletMessage(wallet)}, nil
}

func (server *Server) GetWallet(ctx context.Context, req *walletv1.GetWalletRequest) (*walletv1.GetWalletResponse, error) {
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
	wallet, err := server.app.GetWallet(ctx, userID, walletID)
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &walletv1.GetWalletResponse{Wallet: walletMessage(wallet)}, nil
}

func (server *Server) GetBalance(ctx context.Context, req *walletv1.GetBalanceRequest) (*walletv1.GetBalanceResponse, error) {
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
	wallet, err := server.app.GetBalance(ctx, userID, walletID)
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &walletv1.GetBalanceResponse{
		WalletId:     wallet.ID.String(),
		BalanceCents: wallet.Balance,
		Currency:     wallet.Currency,
	}, nil
}

func (server *Server) InitiateTransfer(ctx context.Context, req *walletv1.InitiateTransferRequest) (*walletv1.InitiateTransferResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	userID, err := userIDFromMetadata(ctx)
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
	if req.GetAmountCents() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_cents must be positive")
	}
	if fromWalletID == toWalletID {
		return nil, status.Error(codes.InvalidArgument, "from_wallet_id and to_wallet_id must differ")
	}
	transfer, err := server.app.CreateTransfer(ctx, userID, fromWalletID, toWalletID, req.GetAmountCents())
	if err != nil {
		return nil, statusFromDomain(err)
	}
	return &walletv1.InitiateTransferResponse{
		TransferId: transfer.ID.String(),
		Status:     transfer.Status,
		Balances: &walletv1.BalanceChange{
			FromBalanceCents: transfer.FromBalance,
			ToBalanceCents:   transfer.ToBalance,
		},
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
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "wallet not found")
	case errors.Is(err, domain.ErrUnsupportedCurrency):
		return status.Error(codes.InvalidArgument, "currency is unsupported")
	case errors.Is(err, domain.ErrInvalidAmount):
		return status.Error(codes.InvalidArgument, "amount is invalid")
	case errors.Is(err, domain.ErrInvalidTransfer):
		return status.Error(codes.InvalidArgument, "transfer is invalid")
	case errors.Is(err, domain.ErrCurrencyMismatch):
		return status.Error(codes.InvalidArgument, "currency mismatch")
	case errors.Is(err, domain.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, "insufficient funds")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

func walletMessage(wallet domain.Wallet) *walletv1.Wallet {
	return &walletv1.Wallet{
		Id:           wallet.ID.String(),
		UserId:       wallet.UserID.String(),
		BalanceCents: wallet.Balance,
		Currency:     wallet.Currency,
		CreatedAt:    timestamppb.New(wallet.CreatedAt),
		UpdatedAt:    timestamppb.New(wallet.UpdatedAt),
	}
}
