// Package orchestrator contains deterministic gates run exclusively by Coordinator.
package orchestrator

import "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"

func Ready(tasks []protocol.TaskSnapshot) []protocol.TaskSnapshot {
	states := map[string]string{}
	for _, t := range tasks {
		states[t.Task.TaskID] = t.Status
	}
	var out []protocol.TaskSnapshot
	for _, t := range tasks {
		if t.Status != "queued" {
			continue
		}
		ok := true
		for _, d := range t.Task.DependsOn {
			if states[d] != "succeeded" {
				ok = false
			}
		}
		if ok {
			out = append(out, t)
		}
	}
	return out
}
func DependencyFailed(t protocol.TaskEnvelope, tasks []protocol.TaskSnapshot) bool {
	states := map[string]string{}
	for _, s := range tasks {
		states[s.Task.TaskID] = s.Status
	}
	for _, d := range t.DependsOn {
		if protocol.Terminal(states[d]) && states[d] != "succeeded" {
			return true
		}
	}
	return false
}
func Aggregate(tasks []protocol.TaskSnapshot, cancelRequested bool) string {
	all, failed, cancelled, running, approval := true, false, false, false, false
	for _, t := range tasks {
		all = all && protocol.Terminal(t.Status)
		failed = failed || t.Status == "failed" || t.Status == "rejected"
		cancelled = cancelled || t.Status == "cancelled"
		running = running || t.Status == "running"
		approval = approval || t.Status == "waiting_approval"
	}
	if all {
		if cancelRequested {
			return "cancelled"
		}
		if failed {
			return "failed"
		}
		if cancelled {
			return "cancelled"
		}
		return "succeeded"
	}
	if running {
		return "running"
	}
	if approval {
		return "waiting_approval"
	}
	return "queued"
}
func Supports(caps []protocol.CapabilityManifest, ref protocol.CapabilityRef) bool {
	for _, c := range caps {
		if c.CapabilityID == ref.ID && c.Authorized && c.MaxConcurrency > 0 && protocol.Matches(ref.Version, c.Version) {
			return true
		}
	}
	return false
}
