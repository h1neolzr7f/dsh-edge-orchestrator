package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Validatable interface{ Validate() error }

// Encode checks a public wire object before serialization.
func Encode(v any) ([]byte, error) {
	check, ok := v.(Validatable)
	if !ok {
		return nil, fmt.Errorf("object has no protocol validator")
	}
	if e := check.Validate(); e != nil {
		return nil, e
	}
	return json.Marshal(v)
}

// DecodeValidated rejects unknown fields and applies the target's semantic validator.
func DecodeValidated(b []byte, dst any) error {
	if e := Decode(b, dst); e != nil {
		return e
	}
	check, ok := dst.(Validatable)
	if !ok {
		return fmt.Errorf("object has no protocol validator")
	}
	return check.Validate()
}
func (h ClientHello) Validate() error {
	if (h.Role != "worker" && h.Role != "controller") || !ValidID(h.DeviceID) || h.AgentVersion == "" || h.LastCoordSeq < 0 || h.LastCoordSeq > MaxSequence || h.LastWorkerSeq < 0 || h.LastWorkerSeq > MaxSequence {
		return Err("INVALID_HELLO", "invalid identity or cursor")
	}
	supported := false
	for _, p := range h.ProtocolVersions {
		supported = supported || p == Version
	}
	if !supported || (h.Role == "worker" && h.LastCoordSeq != 0) || (h.Role == "controller" && h.LastWorkerSeq != 0) {
		return Err("INVALID_HELLO", "role-specific cursor or protocol mismatch")
	}
	return nil
}
func (w ServerWelcome) Validate() error {
	if !ValidID(w.ConnectionID) || w.ServerTime.IsZero() || w.SelectedProtocol != Version || w.HeartbeatInterval < 1 || w.Resume.LastWorkerSeq < 0 || w.Resume.LastWorkerSeq > MaxSequence || w.Resume.AfterCoordSeq < 0 || w.Resume.HighWatermark < 0 {
		return Err("INVALID_WELCOME", "invalid negotiated connection")
	}
	return nil
}
func (d DeviceManifest) Validate() error {
	if !ValidID(d.DeviceID) || d.DisplayName == "" || d.Platform.OS == "" || d.Platform.Arch == "" || d.AgentVersion == "" || d.CapabilitiesRevision < 0 || d.Resources.CPUThreads < 1 {
		return Err("INVALID_DEVICE", "incomplete device manifest")
	}
	return nil
}
func (c CapabilityManifest) Validate() error {
	if c.CapabilityID == "" || !Matches("*", c.Version) || c.PermissionScope == "" || c.MaxConcurrency < 1 || !json.Valid(c.InputSchema) || !json.Valid(c.OutputSchema) {
		return Err("INVALID_CAPABILITY", "invalid capability manifest")
	}
	return nil
}
func (r TaskReceipt) Validate() error {
	if !ValidID(r.TaskID) || !ValidID(r.DeviceID) || r.Status != "accepted" || r.AcceptedAt.IsZero() || r.TaskSeq < 0 || r.TaskSeq > MaxSequence {
		return Err("INVALID_RECEIPT", "invalid accepted receipt")
	}
	return nil
}
func (e TaskEvent) Validate() error {
	if !ValidID(e.EventID) || !ValidID(e.PlanID) || e.Type == "" || e.Timestamp.IsZero() || !json.Valid(e.Payload) || e.CoordSeq < 0 || e.CoordSeq > MaxSequence || e.WorkerSeq < 0 || e.WorkerSeq > MaxSequence || e.TaskSeq < 0 || e.TaskSeq > MaxSequence {
		return Err("INVALID_EVENT", "invalid event")
	}
	if e.DeviceID != "" {
		if !ValidID(e.DeviceID) || !ValidID(e.TaskID) || e.WorkerSeq < 1 || e.TaskSeq < 1 {
			return Err("INVALID_EVENT", "missing worker event identity")
		}
	} else if e.WorkerSeq != 0 || e.TaskSeq != 0 || e.CoordSeq == 0 {
		return Err("INVALID_EVENT", "coordinator event needs global cursor only")
	}
	return nil
}
func (a ArtifactRef) Validate() error {
	if !ValidID(a.ArtifactID) || !ValidID(a.TaskID) || a.Name == "" || a.MediaType == "" || a.SizeBytes < 0 || !hexHash.MatchString(a.SHA256) || !strings.HasPrefix(a.DownloadURI, "/v1/artifacts/") {
		return Err("INVALID_ARTIFACT", "invalid artifact metadata")
	}
	return nil
}
