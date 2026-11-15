package orchestrator

import (
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"testing"
)

func TestDependencyAndCancellation(t *testing.T) {
	tasks := []protocol.TaskSnapshot{{Task: protocol.TaskEnvelope{TaskID: "a"}, Status: "succeeded"}, {Task: protocol.TaskEnvelope{TaskID: "b", DependsOn: []string{"a"}}, Status: "queued"}}
	if len(Ready(tasks)) != 1 {
		t.Fatal("dependency not ready")
	}
	tasks[1].Status = "cancelled"
	if Aggregate(tasks, false) != "cancelled" {
		t.Fatal("step cancel must converge")
	}
	tasks[0].Status = "failed"
	if Aggregate(tasks, false) != "failed" {
		t.Fatal("failure precedence")
	}
	if Aggregate(tasks, true) != "cancelled" {
		t.Fatal("plan cancel precedence")
	}
}
