package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"io"
	"path"
	"regexp"
	"strings"
)

func Decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		return Err("INVALID_PAYLOAD", e.Error())
	}
	var v any
	if d.Decode(&v) != io.EOF {
		return Err("INVALID_PAYLOAD", "trailing JSON")
	}
	return nil
}
func Fingerprint(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var obj any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&obj); e != nil {
		panic(e)
	}
	b, e = json.Marshal(obj)
	if e != nil {
		panic(e)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Matches(constraint, version string) bool {
	c, e := semver.NewConstraint(constraint)
	if e != nil {
		return false
	}
	v, e := semver.NewVersion(version)
	return e == nil && c.Check(v)
}

var hexHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var gitHead = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)
var ident = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$`)

func ValidID(s string) bool { return ident.MatchString(s) }
func SafePath(s string) bool {
	if s == "" || strings.ContainsAny(s, "\\:\x00") || path.IsAbs(s) {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == ".." || p == "" {
			return false
		}
	}
	return path.Clean(s) != "."
}
func (t TaskEnvelope) Validate() error {
	var input map[string]json.RawMessage
	if t.DependsOn == nil || json.Unmarshal(t.Input, &input) != nil || input == nil {
		return Err("INVALID_TASK", "object input and explicit depends_on required")
	}
	if !ValidID(t.TaskID) || t.Capability.ID == "" || len(t.Input) == 0 || !json.Valid(t.Input) {
		return Err("INVALID_TASK", "task_id, capability and JSON input required")
	}
	if _, e := semver.NewConstraint(t.Capability.Version); e != nil {
		return Err("INVALID_TASK", "invalid capability version")
	}
	if t.ReplayPolicy != "" && t.ReplayPolicy != "never" && t.ReplayPolicy != "safe" {
		return Err("INVALID_TASK", "invalid replay_policy")
	}
	if t.Priority != "" && t.Priority != "normal" {
		return Err("INVALID_TASK", "only normal priority is currently supported")
	}
	if w := t.WorkspaceContext; w != nil {
		if w.RootDir == "" || !gitHead.MatchString(w.ExpectedGitHead) || w.Policy != "require_clean_or_match" {
			return Err("INVALID_TASK", "invalid workspace_context")
		}
	}
	outputs := map[string]bool{}
	for _, a := range t.OutputArtifacts {
		if !SafePath(a.Path) || a.Name == "" || a.MediaType == "" || outputs[a.Name] {
			return Err("INVALID_TASK", "invalid output_artifacts")
		}
		outputs[a.Name] = true
	}
	mounts := map[string]bool{}
	for _, a := range t.InputArtifacts {
		if a.ArtifactID == "" || !hexHash.MatchString(a.SHA256) || !SafePath(a.MountPath) || mounts[a.MountPath] {
			return Err("INVALID_TASK", "invalid input_artifacts")
		}
		mounts[a.MountPath] = true
	}
	for _, a := range t.InputArtifactBindings {
		if a.FromTaskID == "" || a.Name == "" || !SafePath(a.MountPath) || mounts[a.MountPath] {
			return Err("INVALID_TASK", "invalid artifact binding")
		}
		mounts[a.MountPath] = true
	}
	return nil
}
func (p PlanEnvelope) Validate() error {
	if !ValidID(p.PlanID) || p.IdempotencyKey == "" || p.Goal == "" || p.ConcurrencyLimit < 1 || p.CreatedAt.IsZero() || len(p.Tasks) == 0 {
		return Err("INVALID_PLAN", "plan id/key/goal/time/tasks and positive concurrency required")
	}
	tasks := map[string]TaskEnvelope{}
	keys := map[string]bool{}
	for _, t := range p.Tasks {
		if e := t.Validate(); e != nil {
			return e
		}
		if _, ok := tasks[t.TaskID]; ok {
			return Err("INVALID_PLAN", "duplicate task_id")
		}
		if t.PlanID != "" && t.PlanID != p.PlanID {
			return Err("INVALID_PLAN", "conflicting plan_id")
		}
		key := t.IdempotencyKey
		if key == "" {
			key = p.PlanID + ":" + t.TaskID
		}
		if keys[key] {
			return Err("INVALID_PLAN", "duplicate task idempotency_key")
		}
		keys[key] = true
		tasks[t.TaskID] = t
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		t, ok := tasks[id]
		if !ok {
			return Err("INVALID_PLAN", "missing dependency "+id)
		}
		if state[id] == 1 {
			return Err("INVALID_PLAN", "dependency cycle")
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		seen := map[string]bool{}
		for _, d := range t.DependsOn {
			if seen[d] {
				return Err("INVALID_PLAN", "duplicate dependency")
			}
			seen[d] = true
			if e := visit(d); e != nil {
				return e
			}
		}
		state[id] = 2
		return nil
	}
	for id := range tasks {
		if e := visit(id); e != nil {
			return e
		}
	}
	for _, t := range p.Tasks {
		anc := map[string]bool{}
		var walk func(string)
		walk = func(id string) {
			for _, d := range tasks[id].DependsOn {
				if !anc[d] {
					anc[d] = true
					walk(d)
				}
			}
		}
		walk(t.TaskID)
		for _, a := range t.InputArtifactBindings {
			if !anc[a.FromTaskID] {
				return Err("INVALID_PLAN", "artifact source is not an ancestor")
			}
		}
	}
	return nil
}
func (e Envelope) Validate() error {
	if e.Protocol != Version || !ValidID(e.MessageID) || e.Kind == "" || e.Sequence < 1 || e.Sequence > MaxSequence || e.Ack < 0 || e.Ack > MaxSequence || e.SentAt.IsZero() || !json.Valid(e.Payload) {
		return fmt.Errorf("invalid protocol envelope")
	}
	return nil
}
func Terminal(s string) bool {
	return s == "succeeded" || s == "failed" || s == "cancelled" || s == "rejected"
}
