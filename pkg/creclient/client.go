package creclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client communicates with the CRE Risk Router.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// New creates a CRE Risk Router client.
func New(endpoint string, timeout time.Duration) *Client {
	return &Client{
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// EvaluateRisk sends a RiskRequest to the CRE Risk Router and returns the decision.
func (c *Client) EvaluateRisk(ctx context.Context, req RiskRequest) (RiskDecision, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return RiskDecision{}, fmt.Errorf("marshal risk request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return RiskDecision{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return RiskDecision{}, fmt.Errorf("send risk request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return RiskDecision{}, fmt.Errorf("CRE returned status %d", resp.StatusCode)
	}

	var decision RiskDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		return RiskDecision{}, fmt.Errorf("decode risk decision: %w", err)
	}

	return decision, nil
}
