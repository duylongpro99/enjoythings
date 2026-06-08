package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
)

var ErrInvalidEvent = errors.New("invalid notification event")

type Adapter interface {
	Send(context.Context, Message) error
}

type Dispatcher struct {
	email  Adapter
	sms    Adapter
	logger *slog.Logger
}

func NewDispatcher(email Adapter, sms Adapter, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{email: email, sms: sms, logger: logger}
}

func (dispatcher *Dispatcher) Dispatch(ctx context.Context, event Event) error {
	message, err := render(event)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if dispatcher.email != nil {
		if err := dispatcher.email.Send(ctx, message); err != nil {
			dispatcher.logOutcome("email", message, "failed", err)
			return err
		}
		dispatcher.logOutcome("email", message, "sent", nil)
	}
	if dispatcher.sms != nil {
		if err := dispatcher.sms.Send(ctx, message); err != nil {
			dispatcher.logOutcome("sms", message, "failed", err)
			return err
		}
		dispatcher.logOutcome("sms", message, "sent", nil)
	}
	return nil
}

func (dispatcher *Dispatcher) logOutcome(channel string, message Message, status string, err error) {
	attrs := []any{
		"channel", channel,
		"status", status,
		"message_id", message.ID,
		"aggregate_id", message.AggregateID,
		"trace_id", message.TraceID,
	}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	dispatcher.logger.Info("notification dispatch handled", attrs...)
}

func render(event Event) (Message, error) {
	switch event.Topic {
	case TopicTxCompleted:
		var payload txCompletedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Message{}, err
		}
		if payload.PaymentID == "" {
			return Message{}, fmt.Errorf("payment_id is required")
		}
		return Message{
			ID:          TopicTxCompleted + ":" + payload.PaymentID,
			AggregateID: payload.PaymentID,
			TraceID:     payload.TraceID,
			Subject:     "Payment completed",
			Body:        fmt.Sprintf("Your payment %s for %s completed successfully.", payload.PaymentID, formatMoney(payload.AmountCents, payload.Currency)),
		}, nil
	case TopicTxFailed:
		var payload txFailedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Message{}, err
		}
		if payload.PaymentID == "" {
			return Message{}, fmt.Errorf("payment_id is required")
		}
		reason := payload.FailureMessage
		if reason == "" {
			reason = payload.FailureCode
		}
		if reason == "" {
			reason = "payment processing failed"
		}
		return Message{
			ID:          TopicTxFailed + ":" + payload.PaymentID,
			AggregateID: payload.PaymentID,
			TraceID:     payload.TraceID,
			Subject:     "Payment failed",
			Body:        fmt.Sprintf("Your payment %s for %s failed: %s.", payload.PaymentID, formatMoney(payload.AmountCents, payload.Currency), reason),
		}, nil
	case TopicTxPaused:
		var payload txPausedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Message{}, err
		}
		if payload.PaymentID == "" {
			return Message{}, fmt.Errorf("payment_id is required")
		}
		reason := payload.Reason
		if reason == "" {
			reason = "additional review is required"
		}
		reason = sanitizeReason(reason)
		return Message{
			ID:          TopicTxPaused + ":" + payload.PaymentID,
			AggregateID: payload.PaymentID,
			TraceID:     payload.TraceID,
			Subject:     "Payment paused for review",
			Body:        fmt.Sprintf("Your payment %s was paused for fraud review with action %s: %s.", payload.PaymentID, payload.Action, reason),
		}, nil
	case TopicUserVerified:
		var payload userVerifiedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Message{}, err
		}
		if payload.UserID == "" {
			return Message{}, fmt.Errorf("user_id is required")
		}
		return Message{
			ID:          TopicUserVerified + ":" + payload.UserID,
			AggregateID: payload.UserID,
			TraceID:     payload.TraceID,
			Subject:     "Verification approved",
			Body:        fmt.Sprintf("Verification %s was approved for user %s.", payload.VerificationID, payload.UserID),
		}, nil
	case TopicUserRejected:
		var payload userRejectedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Message{}, err
		}
		if payload.UserID == "" {
			return Message{}, fmt.Errorf("user_id is required")
		}
		reason := payload.Reason
		if reason == "" {
			reason = "the submitted verification did not pass review"
		}
		return Message{
			ID:          TopicUserRejected + ":" + payload.UserID,
			AggregateID: payload.UserID,
			TraceID:     payload.TraceID,
			Subject:     "Verification rejected",
			Body:        fmt.Sprintf("Verification %s was rejected for user %s: %s.", payload.VerificationID, payload.UserID, reason),
		}, nil
	default:
		return Message{}, fmt.Errorf("unsupported notification topic %q", event.Topic)
	}
}

func formatMoney(amountCents int64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	sign := ""
	if amountCents < 0 {
		sign = "-"
		amountCents = -amountCents
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, amountCents/100, amountCents%100, currency)
}

func sanitizeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return strings.Join(strings.Fields(html.EscapeString(reason)), " ")
}

type txCompletedPayload struct {
	PaymentID   string `json:"payment_id"`
	TraceID     string `json:"trace_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

type txFailedPayload struct {
	PaymentID      string `json:"payment_id"`
	TraceID        string `json:"trace_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	FailureCode    string `json:"failure_code"`
	FailureMessage string `json:"failure_message"`
}

type txPausedPayload struct {
	PaymentID string  `json:"payment_id"`
	TraceID   string  `json:"trace_id"`
	Action    string  `json:"action"`
	RiskScore float64 `json:"risk_score"`
	Reason    string  `json:"reason"`
}

type userVerifiedPayload struct {
	UserID         string `json:"user_id"`
	VerificationID string `json:"verification_id"`
	TraceID        string `json:"trace_id"`
}

type userRejectedPayload struct {
	UserID         string `json:"user_id"`
	VerificationID string `json:"verification_id"`
	TraceID        string `json:"trace_id"`
	Reason         string `json:"reason"`
}
