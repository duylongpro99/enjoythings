package notification

import (
	"context"
	"log/slog"
)

type StubAdapter struct {
	channel string
	logger  *slog.Logger
}

func NewStubEmailAdapter(logger *slog.Logger) *StubAdapter {
	return newStubAdapter("email", logger)
}

func NewStubSMSAdapter(logger *slog.Logger) *StubAdapter {
	return newStubAdapter("sms", logger)
}

func newStubAdapter(channel string, logger *slog.Logger) *StubAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &StubAdapter{channel: channel, logger: logger}
}

func (adapter *StubAdapter) Send(_ context.Context, message Message) error {
	adapter.logger.Info(
		"stub notification accepted",
		"channel", adapter.channel,
		"message_id", message.ID,
		"aggregate_id", message.AggregateID,
		"trace_id", message.TraceID,
		"subject", message.Subject,
	)
	return nil
}
