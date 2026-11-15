package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validPlan() PlanEnvelope {
	return PlanEnvelope{PlanID: "p", IdempotencyKey: "turn:1", Goal: "inspect", ConcurrencyLimit: 2, CreatedAt: time.Now().UTC(), Tasks: []TaskEnvelope{{TaskID: "a", Capability: CapabilityRef{ID: "device.inspect", Version: ">=1.0 <2.0"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}, {TaskID: "b", Capability: CapabilityRef{ID: "report.aggregate", Version: "1.x"}, DependsOn: []string{"a"}, Input: json.RawMessage(`{}`)}}}
}
func TestPlanValidation(t *testing.T) {
	p := validPlan()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*PlanEnvelope)
	}{
		{"cycle", func(p *PlanEnvelope) { p.Tasks[0].DependsOn = []string{"b"} }},
		{"missing dependency", func(p *PlanEnvelope) { p.Tasks[1].DependsOn = []string{"missing"} }},
		{"duplicate id", func(p *PlanEnvelope) { p.Tasks[1].TaskID = "a" }},
		{"concurrency", func(p *PlanEnvelope) { p.ConcurrencyLimit = 0 }},
		{"invalid version", func(p *PlanEnvelope) { p.Tasks[0].Capability.Version = "???" }},
		{"unsafe mount", func(p *PlanEnvelope) {
			p.Tasks[0].InputArtifacts = []InputArtifact{{ArtifactID: "art", SHA256: strings.Repeat("a", 64), MountPath: "../escape"}}
		}},
		{"non ancestor binding", func(p *PlanEnvelope) {
			p.Tasks[0].InputArtifactBindings = []ArtifactBinding{{FromTaskID: "b", Name: "report", MountPath: "inputs/report"}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validPlan()
			tc.mutate(&p)
			if p.Validate() == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
func TestStrictDecode(t *testing.T) {
	var p PlanEnvelope
	if Decode([]byte(`{"unknown":1}`), &p) == nil {
		t.Fatal("unknown field accepted")
	}
	if Decode([]byte(`{} {}`), &p) == nil {
		t.Fatal("trailing JSON accepted")
	}
}
func TestCanonicalIdempotency(t *testing.T) {
	a := json.RawMessage(`{"b":2,"a":1}`)
	b := json.RawMessage(`{"a":1,"b":2}`)
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("object order changes fingerprint")
	}
}
func TestTypedJSONValidation(t *testing.T) {
	for name, v := range map[string]any{"hello": &ClientHello{}, "welcome": &ServerWelcome{}, "device": &DeviceManifest{}, "capability": &CapabilityManifest{}, "receipt": &TaskReceipt{}, "event": &TaskEvent{}, "artifact": &ArtifactRef{}} {
		t.Run(name, func(t *testing.T) {
			if _, e := Encode(v); e == nil {
				t.Fatal("invalid typed payload serialized")
			}
		})
	}
	valid := ClientHello{Role: "worker", DeviceID: "w", AgentVersion: "0.1.0", ProtocolVersions: []string{Version}, PendingMessageIDs: []string{}}
	b, e := Encode(valid)
	if e != nil {
		t.Fatal(e)
	}
	var round ClientHello
	if e = DecodeValidated(b, &round); e != nil {
		t.Fatal(e)
	}
	valid.LastCoordSeq = 1
	if _, e = Encode(valid); e == nil {
		t.Fatal("worker accepted controller cursor")
	}
}
func TestTaskRequiresObjectInputAndExplicitDependencies(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `1`} {
		p := validPlan()
		p.Tasks[0].Input = json.RawMessage(input)
		if p.Validate() == nil {
			t.Fatalf("non-object input accepted: %s", input)
		}
	}
	p := validPlan()
	p.Tasks[0].DependsOn = nil
	if p.Validate() == nil {
		t.Fatal("missing depends_on accepted")
	}
}
