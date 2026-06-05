package client

import (
	"context"

	verificationv1 "enjoythings/services/gen/verification/v1"
	"enjoythings/services/internal/verification"
)

type VerificationClient struct {
	client verificationv1.VerificationServiceClient
}

func NewVerificationClient(client verificationv1.VerificationServiceClient) *VerificationClient {
	return &VerificationClient{client: client}
}

func (client *VerificationClient) Submit(ctx context.Context, cmd verification.SubmitCommand) (verification.Record, error) {
	resp, err := client.client.SubmitVerification(ctx, &verificationv1.SubmitVerificationRequest{
		PaymentId:      cmd.PaymentID,
		IdempotencyKey: cmd.IdempotencyKey,
		TraceId:        cmd.TraceID,
		UserId:         cmd.UserID,
		VerificationId: cmd.VerificationID,
		Decision:       cmd.Decision,
		Reason:         cmd.Reason,
	})
	if err != nil {
		return verification.Record{}, err
	}
	record := verification.Record{
		VerificationID: resp.GetVerificationId(),
		UserID:         resp.GetUserId(),
		Status:         resp.GetStatus(),
	}
	if resp.GetDecidedAt() != nil {
		record.DecidedAt = resp.GetDecidedAt().AsTime()
	}
	return record, nil
}

func (client *VerificationClient) GetStatus(ctx context.Context, userID string) (verification.Record, error) {
	resp, err := client.client.GetStatus(ctx, &verificationv1.GetStatusRequest{UserId: userID})
	if err != nil {
		return verification.Record{}, err
	}
	record := verification.Record{
		UserID:         resp.GetUserId(),
		Status:         resp.GetStatus(),
		VerificationID: resp.GetVerificationId(),
		Reason:         resp.GetReason(),
	}
	if resp.GetUpdatedAt() != nil {
		record.UpdatedAt = resp.GetUpdatedAt().AsTime()
	}
	return record, nil
}
