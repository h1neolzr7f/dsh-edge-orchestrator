package protocol

import "time"

// Approval binds a controller decision to the immutable stored task envelope.
type Approval struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	TaskID         string     `json:"task_id"`
	DeviceID       string     `json:"device_id,omitempty"`
	Scope          string     `json:"scope"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}
type ApprovalDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}
type PairingRequest struct {
	DeviceHint     string `json:"device_hint,omitempty"`
	CoordinatorURL string `json:"coordinator_url"`
}
type Pairing struct {
	PairingCode    string    `json:"pairing_code"`
	CoordinatorURL string    `json:"coordinator_url"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type PairingClaim struct {
	PairingCode string `json:"pairing_code"`
	DeviceID    string `json:"device_id"`
	DisplayName string `json:"display_name,omitempty"`
}
type PairingCredential struct {
	DeviceID    string `json:"device_id"`
	WorkerToken string `json:"worker_token"`
}
