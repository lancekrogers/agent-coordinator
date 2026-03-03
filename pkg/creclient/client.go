package creclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrRequestFailed indicates an HTTP transport/request failure to CRE.
	ErrRequestFailed = errors.New("creclient: request failed")

	// ErrUnexpectedStatus indicates CRE returned a non-200 response.
	ErrUnexpectedStatus = errors.New("creclient: unexpected status")

	// ErrDecodeFailed indicates CRE returned an invalid JSON body.
	ErrDecodeFailed = errors.New("creclient: decode failed")
)

// Client communicates with the CRE Risk Router.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// New creates a CRE Risk Router client.
func New(endpoint string, timeout time.Duration) *Client {
	return &Client{
		endpoint:   normalizeEndpoint(endpoint),
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
		return RiskDecision{}, fmt.Errorf("%w: send risk request: %v", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return RiskDecision{}, fmt.Errorf("%w: CRE returned status %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	var decision RiskDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		return RiskDecision{}, fmt.Errorf("%w: decode risk decision: %v", ErrDecodeFailed, err)
	}

	return decision, nil
}

func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return endpoint
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return endpoint
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = "/evaluate-risk"
	}

	return u.String()
}
