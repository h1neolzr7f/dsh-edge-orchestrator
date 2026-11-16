// Generates the shared JSON Schema from Go wire types. Run from the repository root.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"reflect"
	"strings"
	"time"
)

var defs = map[string]any{}

func schema(t reflect.Type) map[string]any {
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeOf([]byte{}) {
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{}
	}
	if t.Kind() == reflect.Pointer {
		return schema(t.Elem())
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schema(t.Elem())}
	case reflect.Struct:
		name := t.Name()
		if _, ok := defs[name]; !ok {
			defs[name] = nil
			props := map[string]any{}
			required := []string{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Anonymous {
					embedded := schema(f.Type)
					_ = embedded
					if d, ok := defs[f.Type.Name()].(map[string]any); ok {
						for k, v := range d["properties"].(map[string]any) {
							props[k] = v
						}
						required = append(required, d["required"].([]string)...)
					}
					continue
				}
				tag := f.Tag.Get("json")
				parts := strings.Split(tag, ",")
				key := parts[0]
				if key == "-" {
					continue
				}
				if key == "" {
					key = f.Name
				}
				s := schema(f.Type)
				if strings.HasSuffix(key, "_seq") || key == "sequence" || key == "ack" || key == "high_watermark" {
					s["minimum"] = 0
					s["maximum"] = protocol.MaxSequence
				}
				if key == "sequence" || key == "concurrency_limit" || key == "max_concurrency" {
					s["minimum"] = 1
				}
				if key == "pending_reason" {
					s["enum"] = []string{"", "waiting_dependencies", "scheduling_paused", "concurrency_limit", "device_offline", "capability_unavailable", "device_busy"}
				}
				props[key] = s
				if !strings.Contains(tag, "omitempty") {
					required = append(required, key)
				}
			}
			defs[name] = map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	panic(t.String())
}
func main() {
	for _, v := range []any{protocol.Envelope{}, protocol.ClientHello{}, protocol.ServerWelcome{}, protocol.DeviceManifest{}, protocol.CapabilityManifest{}, protocol.PlanEnvelope{}, protocol.TaskEnvelope{}, protocol.TaskReceipt{}, protocol.TaskEvent{}, protocol.ArtifactRef{}, protocol.ArtifactUploadRequest{}, protocol.ArtifactUploadSession{}, protocol.PlanSnapshot{}, protocol.FleetSnapshot{}, protocol.EventPage{}, protocol.EventPayload{}, protocol.DeviceRegistration{}, protocol.EventAck{}, protocol.TaskRequest{}, protocol.PlanAccepted{}, protocol.TaskReconcileSnapshot{}} {
		schema(reflect.TypeOf(v))
	}
	defs["TaskEnvelope"].(map[string]any)["properties"].(map[string]any)["input"] = map[string]any{"type": "object"}
	envelope := defs["Envelope"].(map[string]any)["properties"].(map[string]any)
	envelope["protocol"] = map[string]any{"const": protocol.Version}
	plan := defs["PlanEnvelope"].(map[string]any)["properties"].(map[string]any)
	plan["tasks"].(map[string]any)["minItems"] = 1
	b, e := json.MarshalIndent(map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$id": "urn:dsh-edge:protocol:1", "$ref": "#/$defs/Envelope", "$defs": defs}, "", "  ")
	if e != nil {
		panic(e)
	}
	fmt.Println(string(b))
}
