package creclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEvaluateRisk_Approved(t *testing.T) {
	expected := RiskDecision{
		Approved:       true,
		MaxPositionUSD: 810000000,
		MaxSlippageBps: 500,
		TTLSeconds:     300,
		Reason:         "approved",
		ChainlinkPrice: 200000000000,
		Timestamp:      time.Now().Unix(),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type")
		}

		var req RiskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.AgentID != "test-agent" {
			t.Errorf("expected agent_id test-agent, got %s", req.AgentID)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	client := New(srv.URL, 5*time.Second)
	decision, err := client.EvaluateRisk(context.Background(), RiskRequest{
		AgentID:           "test-agent",
		TaskID:            "task-1",
		Signal:            "buy",
		SignalConfidence:  0.85,
		RiskScore:         10,
		MarketPair:        "ETH/USD",
		RequestedPosition: 1000000000,
		Timestamp:         time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("EvaluateRisk: %v", err)
	}
	if !decision.Approved {
		t.Error("expected approved=true")
	}
	if decision.MaxPositionUSD != 810000000 {
		t.Errorf("expected MaxPositionUSD=810000000, got %d", decision.MaxPositionUSD)
	}
}

func TestEvaluateRisk_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := New(srv.URL, 5*time.Second)
	_, err := client.EvaluateRisk(context.Background(), RiskRequest{})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestEvaluateRisk_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(srv.URL, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.EvaluateRisk(ctx, RiskRequest{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEvaluateRisk_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	client := New(srv.URL, 5*time.Second)
	_, err := client.EvaluateRisk(context.Background(), RiskRequest{})
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
}
