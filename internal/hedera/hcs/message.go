package hcs

import (
	"encoding/json"
	"time"
)

// MessageType identifies the kind of protocol message in an envelope.
type MessageType string

const (
	// MessageTypeTaskAssignment is sent by the coordinator to assign a task to an agent.
	MessageTypeTaskAssignment MessageType = "task_assignment"

	// MessageTypeStatusUpdate is sent by an agent to report progress on a task.
	MessageTypeStatusUpdate MessageType = "status_update"

	// MessageTypeTaskResult is sent by an agent when a task is complete.
	MessageTypeTaskResult MessageType = "task_result"

	// MessageTypeHeartbeat is sent periodically to signal agent liveness.
	MessageTypeHeartbeat MessageType = "heartbeat"

	// MessageTypeQualityGate is sent by the coordinator for quality gate decisions.
	MessageTypeQualityGate MessageType = "quality_gate"

	// MessageTypePaymentSettled is sent after HTS payment is completed.
	MessageTypePaymentSettled MessageType = "payment_settled"

	// MessageTypePnLReport is sent by the DeFi agent with profit/loss data.
	MessageTypePnLReport MessageType = "pnl_report"

	// MessageTypeRiskCheckRequested is sent when the coordinator requests a CRE risk evaluation.
	MessageTypeRiskCheckRequested MessageType = "risk_check_requested"

	// MessageTypeRiskCheckApproved is sent when CRE approves a trade.
	MessageTypeRiskCheckApproved MessageType = "risk_check_approved"

	// MessageTypeRiskCheckDenied is sent when CRE denies a trade.
	MessageTypeRiskCheckDenied MessageType = "risk_check_denied"
)

// Envelope is the standard message format for all festival protocol messages
// sent through HCS topics. Every message on the wire uses this structure.
type Envelope struct {
	// Type identifies what kind of message this is.
	Type MessageType `json:"type"`

	// Sender is the identifier of the agent or coordinator that sent this message.
	Sender string `json:"sender"`

	// Recipient is the intended recipient (empty string means broadcast to all subscribers).
	Recipient string `json:"recipient,omitempty"`

	// TaskID references the festival task this message relates to (if applicable).
	TaskID string `json:"task_id,omitempty"`

	// SequenceNum is a monotonically increasing number for ordering within a sender.
	SequenceNum uint64 `json:"sequence_num"`

	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp"`

	// Payload contains the type-specific message data as raw JSON.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Marshal serializes the envelope to JSON bytes for publishing to HCS.
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes JSON bytes from HCS into an Envelope.
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
