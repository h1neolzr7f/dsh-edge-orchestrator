// Package protocol defines the dsh-edge/1 wire contract shared by every component.
package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

const Version = "dsh-edge/1"
const MaxSequence int64 = 9007199254740991

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string       { return e.Code + ": " + e.Message }
func Err(code, message string) error { return &Error{code, message} }
func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

type Envelope struct {
	Protocol      string          `json:"protocol"`
	MessageID     string          `json:"message_id"`
	Kind          string          `json:"kind"`
	SentAt        time.Time       `json:"sent_at"`
	CorrelationID string          `json:"correlation_id"`
	Sequence      int64           `json:"sequence"`
	Ack           int64           `json:"ack"`
	Payload       json.RawMessage `json:"payload"`
}
type ClientHello struct {
	Role              string   `json:"role"`
	DeviceID          string   `json:"device_id"`
	AgentVersion      string   `json:"agent_version"`
	ProtocolVersions  []string `json:"protocol_versions"`
	LastCoordSeq      int64    `json:"last_coord_seq,omitempty"`
	LastWorkerSeq     int64    `json:"last_worker_seq,omitempty"`
	PendingMessageIDs []string `json:"pending_message_ids"`
}
type Resume struct {
	AfterCoordSeq    int64 `json:"after_coord_seq,omitempty"`
	HighWatermark    int64 `json:"high_watermark,omitempty"`
	SnapshotRequired bool  `json:"snapshot_required,omitempty"`
	LastWorkerSeq    int64 `json:"last_worker_seq"`
}
type ServerWelcome struct {
	ConnectionID      string    `json:"connection_id"`
	ServerTime        time.Time `json:"server_time"`
	SelectedProtocol  string    `json:"selected_protocol"`
	HeartbeatInterval int       `json:"heartbeat_interval"`
	Resume            Resume    `json:"resume"`
}
type Platform struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Version string `json:"version"`
}
type Resources struct {
	CPUThreads    int    `json:"cpu_threads"`
	MemoryBytes   uint64 `json:"memory_bytes"`
	DiskFreeBytes uint64 `json:"disk_free_bytes"`
}
type DeviceManifest struct {
	DeviceID             string    `json:"device_id"`
	DisplayName          string    `json:"display_name"`
	Platform             Platform  `json:"platform"`
	AgentVersion         string    `json:"agent_version"`
	Labels               []string  `json:"labels"`
	Resources            Resources `json:"resources"`
	CapabilitiesRevision int       `json:"capabilities_revision"`
}
type CapabilityManifest struct {
	CapabilityID    string          `json:"capability_id"`
	Version         string          `json:"version"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
	PermissionScope string          `json:"permission_scope"`
	ResourceClass   string          `json:"resource_class"`
	MaxConcurrency  int             `json:"max_concurrency"`
	Discovery       string          `json:"discovery"`
	Authorized      bool            `json:"authorized"`
}
type DeviceRegistration struct {
	Manifest     DeviceManifest       `json:"manifest"`
	Capabilities []CapabilityManifest `json:"capabilities"`
}
type DeviceSnapshot struct {
	DeviceManifest
	Capabilities []CapabilityManifest `json:"capabilities"`
	Online       bool                 `json:"online"`
}
type CapabilityRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type WorkspaceContext struct {
	RootDir         string `json:"root_dir"`
	ExpectedGitHead string `json:"expected_git_head"`
	PatchArtifactID string `json:"patch_artifact_id,omitempty"`
	Policy          string `json:"policy"`
}
type InputArtifact struct {
	ArtifactID string `json:"artifact_id"`
	SHA256     string `json:"sha256"`
	MountPath  string `json:"mount_path"`
}
type ArtifactBinding struct {
	FromTaskID string `json:"from_task_id"`
	Name       string `json:"name"`
	MountPath  string `json:"mount_path"`
}
type OutputArtifact struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
}
type TaskEnvelope struct {
	RequiresApproval      bool              `json:"requires_approval,omitempty"`
	ApprovalReason        string            `json:"approval_reason,omitempty"`
	OutputArtifacts       []OutputArtifact  `json:"output_artifacts,omitempty"`
	TaskID                string            `json:"task_id"`
	PlanID                string            `json:"plan_id,omitempty"`
	IdempotencyKey        string            `json:"idempotency_key,omitempty"`
	TargetDeviceID        string            `json:"target_device_id,omitempty"`
	Capability            CapabilityRef     `json:"capability"`
	Input                 json.RawMessage   `json:"input"`
	DependsOn             []string          `json:"depends_on"`
	WorkspaceContext      *WorkspaceContext `json:"workspace_context,omitempty"`
	InputArtifacts        []InputArtifact   `json:"input_artifacts,omitempty"`
	InputArtifactBindings []ArtifactBinding `json:"input_artifact_bindings,omitempty"`
	Priority              string            `json:"priority,omitempty"`
	Deadline              *time.Time        `json:"deadline,omitempty"`
	ReplayPolicy          string            `json:"replay_policy,omitempty"`
	RequestedPermissions  []string          `json:"requested_permissions,omitempty"`
}
type PlanEnvelope struct {
	PlanID           string         `json:"plan_id"`
	IdempotencyKey   string         `json:"idempotency_key"`
	Goal             string         `json:"goal"`
	ConcurrencyLimit int            `json:"concurrency_limit"`
	CreatedAt        time.Time      `json:"created_at"`
	Tasks            []TaskEnvelope `json:"tasks"`
}
type TaskReceipt struct {
	TaskID     string    `json:"task_id"`
	DeviceID   string    `json:"device_id"`
	Status     string    `json:"status"`
	AcceptedAt time.Time `json:"accepted_at"`
	TaskSeq    int64     `json:"task_seq"`
}
type TaskEvent struct {
	EventID   string          `json:"event_id"`
	PlanID    string          `json:"plan_id"`
	TaskID    string          `json:"task_id,omitempty"`
	DeviceID  string          `json:"device_id,omitempty"`
	CoordSeq  int64           `json:"coord_seq,omitempty"`
	WorkerSeq int64           `json:"worker_seq,omitempty"`
	TaskSeq   int64           `json:"task_seq,omitempty"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}
type ArtifactRef struct {
	ArtifactID  string `json:"artifact_id"`
	TaskID      string `json:"task_id"`
	Name        string `json:"name"`
	MediaType   string `json:"media_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	DownloadURI string `json:"download_uri"`
}
type EventAck struct {
	LastWorkerSeq int64 `json:"last_worker_seq"`
}
type TaskRequest struct {
	TaskID string `json:"task_id"`
}
type EventPayload struct {
	Data      []byte          `json:"data,omitempty"`
	Message   string          `json:"message,omitempty"`
	Completed int             `json:"completed,omitempty"`
	Total     int             `json:"total,omitempty"`
	Stream    string          `json:"stream,omitempty"`
	Offset    int64           `json:"offset,omitempty"`
	Length    int64           `json:"length,omitempty"`
	Text      string          `json:"text,omitempty"`
	Status    string          `json:"status,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}
type TaskSnapshot struct {
	PendingReason string          `json:"pending_reason,omitempty"`
	Task          TaskEnvelope    `json:"task"`
	Status        string          `json:"status"`
	TaskSeq       int64           `json:"task_seq"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *Error          `json:"error,omitempty"`
	Artifacts     []ArtifactRef   `json:"artifacts"`
}
type PlanSnapshot struct {
	Plan             PlanEnvelope   `json:"plan"`
	Status           string         `json:"status"`
	SchedulingPaused bool           `json:"scheduling_paused"`
	CancelRequested  bool           `json:"cancel_requested"`
	Tasks            []TaskSnapshot `json:"tasks"`
	SnapshotCoordSeq int64          `json:"snapshot_coord_seq"`
}
type PlanAccepted struct {
	PlanID   string `json:"plan_id"`
	Status   string `json:"status"`
	CoordSeq int64  `json:"coord_seq"`
}
type EventPage struct {
	Events        []TaskEvent `json:"events"`
	NextCoordSeq  int64       `json:"next_coord_seq"`
	HighWatermark int64       `json:"high_watermark"`
	HasMore       bool        `json:"has_more"`
}
type FleetSnapshot struct {
	Devices          []DeviceSnapshot `json:"devices"`
	Plans            []PlanSnapshot   `json:"plans"`
	SnapshotCoordSeq int64            `json:"snapshot_coord_seq"`
}

// TaskReconcileSnapshot is the worker-side receipt/state reply, not a Plan card.
type TaskReconcileSnapshot struct {
	TaskID   string `json:"task_id"`
	DeviceID string `json:"device_id"`
	Status   string `json:"status,omitempty"`
	TaskSeq  int64  `json:"task_seq"`
	Error    *Error `json:"error,omitempty"`
}

// ArtifactUploadRequest identifies immutable bytes and their task provenance.
type ArtifactUploadRequest struct {
	PlanID    string `json:"plan_id"`
	TaskID    string `json:"task_id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// ArtifactUploadSession contains the durable server-confirmed resume position.
type ArtifactUploadSession struct {
	UploadID        string `json:"upload_id"`
	ArtifactID      string `json:"artifact_id"`
	ChunkSize       int    `json:"chunk_size"`
	ConfirmedOffset int64  `json:"confirmed_offset"`
}
