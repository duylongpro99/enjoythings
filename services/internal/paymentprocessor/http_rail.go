package paymentprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPRail struct {
	baseURL string
	client  *http.Client
}

func NewHTTPRail(baseURL string, client *http.Client) *HTTPRail {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &HTTPRail{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (rail *HTTPRail) Charge(ctx context.Context, charge RailChargeRequest) (RailResult, error) {
	payload, err := json.Marshal(charge)
	if err != nil {
		return RailResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rail.baseURL+"/charge", bytes.NewReader(payload))
	if err != nil {
		return RailResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := rail.client.Do(req)
	if err != nil {
		return RailResult{}, RetryableRailError("rail_unavailable", err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var body struct {
			ProcessorPaymentID string    `json:"processor_payment_id"`
			CompletedAt        time.Time `json:"completed_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return RailResult{}, TerminalRailError("invalid_rail_response", "invalid rail success response")
		}
		if body.ProcessorPaymentID == "" {
			return RailResult{}, TerminalRailError("invalid_rail_response", "processor_payment_id is required")
		}
		return RailResult{ProcessorPaymentID: body.ProcessorPaymentID, CompletedAt: body.CompletedAt}, nil
	}

	failure := decodeRailFailure(resp.Body)
	if failure.Code == "" {
		failure.Code = fmt.Sprintf("rail_http_%d", resp.StatusCode)
	}
	if failure.Message == "" {
		failure.Message = http.StatusText(resp.StatusCode)
	}
	failure.Retryable = resp.StatusCode >= 500
	return RailResult{}, RailError{Failure: failure}
}

func decodeRailFailure(body io.Reader) RailFailure {
	var failure RailFailure
	_ = json.NewDecoder(body).Decode(&failure)
	return failure
}
