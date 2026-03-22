package hub

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// RunDemoEvents generates realistic agent events and broadcasts them
// to the hub for demo mode. This simulates what agents would publish
// through HCS in live mode, but routes through the WebSocket hub directly.
func RunDemoEvents(ctx context.Context, h *Hub) {
	// Agent state for realistic data generation
	ethPrice := 3200.0 + (rand.Float64()-0.5)*100
	gpuUtil := 75.0
	memUtil := 45.0
	activeJobs := 3
	tradeCount := 0
	jobCounter := 5670
	totalInferences := 5678

	// Heartbeats every 5s
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ts := time.Now().UTC().Format(time.RFC3339)

				// Coordinator heartbeat
				h.Publish(DaemonEvent{
					Type: "heartbeat", AgentID: "coord-001", AgentName: "coordinator",
					Timestamp: ts,
					Payload: map[string]interface{}{
						"status":             "running",
						"monitoringSequence": "05_defi_pnl",
					},
				})

				// Inference heartbeat with GPU metrics
				gpuUtil = clamp(gpuUtil+(rand.Float64()-0.5)*10+(78-gpuUtil)*0.1, 20, 98)
				memUtil = clamp(memUtil+(rand.Float64()-0.5)*5+(45-memUtil)*0.1, 15, 85)
				h.Publish(DaemonEvent{
					Type: "heartbeat", AgentID: "inf-001", AgentName: "inference",
					Timestamp: ts,
					Payload: map[string]interface{}{
						"gpuUtilization":    round(gpuUtil, 1),
						"memoryUtilization": round(memUtil, 1),
						"activeJobs":        activeJobs,
						"avgLatencyMs":      80 + rand.Intn(80),
						"totalInferences":   totalInferences,
						"storage": map[string]interface{}{
							"totalStorageGb": 50.0,
							"usedStorageGb":  12.4 + float64(totalInferences)*0.001,
							"objectCount":    1234 + totalInferences,
						},
						"inft": map[string]interface{}{
							"tokenId":        "0.0.98765",
							"status":         "active",
							"modelName":      "llama-3-8b",
							"inferenceCount": totalInferences,
							"lastActive":     ts,
						},
					},
				})

				// DeFi heartbeat
				h.Publish(DaemonEvent{
					Type: "heartbeat", AgentID: "defi-001", AgentName: "defi",
					Timestamp: ts,
					Payload: map[string]interface{}{
						"status": "running",
					},
				})
			}
		}
	}()

	// Trade results every 15-30s
	go func() {
		time.Sleep(3 * time.Second) // First trade quickly
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ethPrice += (rand.Float64() - 0.48) * 15
			ethPrice = clamp(ethPrice, 2800, 3600)
			tradeCount++

			side := "buy"
			if rand.Float64() > 0.5 {
				side = "sell"
			}
			amount := round(rand.Float64()*3+0.1, 4)
			pnl := round((rand.Float64()-0.35)*80, 2)
			gasCost := round(rand.Float64()*4, 2)

			h.Publish(DaemonEvent{
				Type: "task_result", AgentID: "defi-001", AgentName: "defi",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Payload: map[string]interface{}{
					"txHash":  fmt.Sprintf("0x%x", rand.Int63()),
					"pair":    []string{"ETH/USDC", "WETH/DAI", "USDC/USDT"}[tradeCount%3],
					"side":    side,
					"amount":  amount,
					"price":   round(ethPrice, 2),
					"pnl":     pnl,
					"gasCost": gasCost,
					"tradeId": fmt.Sprintf("trade-%d", tradeCount),
				},
			})

			time.Sleep(time.Duration(15+rand.Intn(15)) * time.Second)
		}
	}()

	// Inference results every 10-20s
	go func() {
		time.Sleep(2 * time.Second)
		models := []string{"llama-3-8b", "mistral-7b", "phi-3-mini"}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			jobCounter++
			totalInferences++
			h.Publish(DaemonEvent{
				Type: "task_result", AgentID: "inf-001", AgentName: "inference",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Payload: map[string]interface{}{
					"jobId":        fmt.Sprintf("job-%d", jobCounter),
					"model":        models[jobCounter%len(models)],
					"status":       "completed",
					"inputTokens":  80 + rand.Intn(400),
					"outputTokens": 30 + rand.Intn(300),
					"latencyMs":    40 + rand.Intn(180),
				},
			})

			time.Sleep(time.Duration(10+rand.Intn(10)) * time.Second)
		}
	}()

	// Risk check flow every 20-40s
	go func() {
		time.Sleep(4 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			taskID := fmt.Sprintf("task-%x", rand.Int31())
			ts := time.Now().UTC().Format(time.RFC3339)

			// Request
			h.Publish(DaemonEvent{
				Type: "risk_check_requested", AgentID: "coord-001", AgentName: "coordinator",
				Timestamp: ts,
				Payload: map[string]interface{}{
					"task_id":   taskID,
					"recipient": "defi-001",
					"payload":   map[string]interface{}{"reason": "requested"},
				},
			})

			// Task assignment with CRE constraints
			h.Publish(DaemonEvent{
				Type: "task_assignment", AgentID: "coord-001", AgentName: "coordinator",
				Timestamp: ts,
				Payload: map[string]interface{}{
					"task_id": taskID,
					"payload": map[string]interface{}{
						"cre_decision": map[string]interface{}{
							"max_position_usd": 500000000 + rand.Intn(500000000),
							"max_slippage_bps": 10 + rand.Intn(40),
						},
					},
				},
			})

			time.Sleep(time.Duration(2+rand.Intn(3)) * time.Second)

			// Decision
			approved := rand.Float64() < 0.75
			decisionType := "risk_check_approved"
			reason := "approved"
			if !approved {
				decisionType = "risk_check_denied"
				reasons := []string{"signal_confidence_below_threshold", "cre_unreachable", "position_limit_exceeded"}
				reason = reasons[rand.Intn(len(reasons))]
			}

			h.Publish(DaemonEvent{
				Type: decisionType, AgentID: "coord-001", AgentName: "coordinator",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Payload: map[string]interface{}{
					"task_id":   taskID,
					"recipient": "defi-001",
					"payload":   map[string]interface{}{"reason": reason},
				},
			})

			time.Sleep(time.Duration(20+rand.Intn(20)) * time.Second)
		}
	}()
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func round(v float64, decimals int) float64 {
	mul := 1.0
	for i := 0; i < decimals; i++ {
		mul *= 10
	}
	return float64(int(v*mul)) / mul
}
