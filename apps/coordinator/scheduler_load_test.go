package coordinator

import (
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"modernc.org/sqlite"
)

func TestScheduleLoadsActiveTasksOnceAcrossPlans(t *testing.T) {
	// A real SQLite view counts payload reads without replacing the scheduler or
	// its database. Each task is read once for global load and once for its plan.
	reads := 0
	fn := protocol.NewID("count_payload")
	if err := sqlite.RegisterScalarFunction(fn, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		reads++
		return args[0], nil
	}); err != nil {
		t.Fatal(err)
	}
	s := setup(t)
	const plans = 4
	for i := 0; i < plans; i++ {
		p := samplePlan()
		p.PlanID = fmt.Sprintf("p%d", i)
		p.IdempotencyKey = p.PlanID
		p.Tasks[0].TaskID = fmt.Sprintf("t%d", i)
		if _, err := s.Submit(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE tasks SET status='accepted'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks RENAME TO counted_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE VIEW tasks AS SELECT id,plan_id,ikey,` + fn + `(payload) AS payload,status,task_seq,result,error,last_dispatch,pending_reason FROM counted_tasks`); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	err := s.schedule()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2*plans {
		t.Fatalf("active payload reads=%d want=%d (one global scan plus each plan)", reads, 2*plans)
	}
}
