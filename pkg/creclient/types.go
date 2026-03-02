package creclient

// RiskRequest matches the CRE Risk Router input format.
type RiskRequest struct {
	AgentID           string  `json:"agent_id"`
	TaskID            string  `json:"task_id"`
	Signal            string  `json:"signal"`              // buy, sell, hold
	SignalConfidence  float64 `json:"signal_confidence"`    // 0.0-1.0
	RiskScore         int     `json:"risk_score"`           // 0-100
	MarketPair        string  `json:"market_pair"`          // e.g. ETH/USD
	RequestedPosition float64 `json:"requested_position"`   // 6-decimal USD
	Timestamp         int64   `json:"timestamp"`            // Unix seconds
}

// RiskDecision matches the CRE Risk Router output format.
type RiskDecision struct {
	Approved       bool   `json:"approved"`
	MaxPositionUSD uint64 `json:"max_position_usd"`
	MaxSlippageBps uint64 `json:"max_slippage_bps"`
	TTLSeconds     uint64 `json:"ttl_seconds"`
	Reason         string `json:"reason"`
	ChainlinkPrice uint64 `json:"chainlink_price"`
	Timestamp      int64  `json:"timestamp"`
}
