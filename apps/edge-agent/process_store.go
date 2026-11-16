package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

var processColumns = map[string]string{"pid": "INTEGER", "process_start_time_ns": "INTEGER", "os_boot_id": "TEXT", "stdout_path": "TEXT", "stderr_path": "TEXT", "stdout_offset": "INTEGER NOT NULL DEFAULT 0", "stderr_offset": "INTEGER NOT NULL DEFAULT 0", "exit_status_path": "TEXT", "job_name": "TEXT"}

func ensureProcessColumns(db *sql.DB) error {
	rows, e := db.Query(`PRAGMA table_info(local_task_ledger)`)
	if e != nil {
		return e
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, n, pk int
		var name, kind string
		var def any
		if e = rows.Scan(&cid, &name, &kind, &n, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		have[name] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for name, kind := range processColumns {
		if !have[name] {
			if _, e = db.Exec(`ALTER TABLE local_task_ledger ADD COLUMN ` + name + ` ` + kind); e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *Agent) saveProcess(t protocol.TaskEnvelope, ready runnerReady, spec runnerSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, e := a.db.Exec(`UPDATE local_task_ledger SET pid=?,process_start_time_ns=?,os_boot_id=?,stdout_path=?,stderr_path=?,exit_status_path=?,job_name=? WHERE id=?`, ready.Identity.PID, ready.Identity.StartTimeNS, ready.Identity.BootID, filepath.Join(spec.Dir, "stdout.log"), filepath.Join(spec.Dir, "stderr.log"), filepath.Join(spec.Dir, "exit.json"), spec.JobName, t.TaskID)
	return e
}
func (a *Agent) tailLogs(t protocol.TaskEnvelope, dir string, final ...bool) error {
	for _, stream := range []string{"stdout", "stderr"} {
		if e := a.tailStream(t, dir, stream, len(final) > 0 && final[0]); e != nil {
			return e
		}
	}
	return nil
}

func (a *Agent) tailStream(t protocol.TaskEnvelope, dir, stream string, final bool) error {
	file, e := os.Open(filepath.Join(dir, stream+".log"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer file.Close()
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	column := stream + "_offset"
	var offset int64
	if e = tx.QueryRow(`SELECT `+column+` FROM local_task_ledger WHERE id=?`, t.TaskID).Scan(&offset); e != nil {
		return e
	}
	initial := offset
	buffer := make([]byte, 4096+utf8.UTFMax-1)
	// A bounded batch keeps event/offset atomicity while avoiding one durable
	// SQLite commit per 4 KiB (especially costly on Windows).
	for chunk := 0; chunk < 16; chunk++ {
		n, readErr := file.ReadAt(buffer, offset)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if n == 0 {
			break
		}
		count := n
		if count > 4096 {
			count = 4096
		}
		start := count - 1
		for start > 0 && !utf8.RuneStart(buffer[start]) && count-start < utf8.UTFMax {
			start--
		}
		_, size := utf8.DecodeRune(buffer[start:n])
		if start+size > count {
			count = start
		}
		if !final && !utf8.FullRune(buffer[start:count]) {
			count = start
		}
		if count == 0 {
			break
		}
		if e = appendEvent(tx, a.cfg.DeviceID, t, "task.log", protocol.EventPayload{Stream: stream, Offset: offset, Length: int64(count), Text: string(buffer[:count]), Data: buffer[:count]}, "running"); e != nil {
			return e
		}
		offset += int64(count)
	}
	if offset == initial {
		return nil
	}
	if _, e = tx.Exec(`UPDATE local_task_ledger SET `+column+`=? WHERE id=?`, offset, t.TaskID); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *Agent) canResumeProcess(t protocol.TaskEnvelope) bool {
	var spec runnerSpec
	if readJSON(filepath.Join(a.taskDir(t.TaskID), "spec.json"), &spec) != nil || spec.TaskID != t.TaskID || spec.Fingerprint != protocol.Fingerprint(t) {
		return false
	}
	var exit runnerExit
	if readJSON(filepath.Join(spec.Dir, "exit.json"), &exit) == nil && exit.Nonce == spec.Nonce {
		return true
	}
	var ready runnerReady
	return readJSON(filepath.Join(spec.Dir, "ready.json"), &ready) == nil && ready.Nonce == spec.Nonce && sameProcess(ready.Identity)
}
func processSummary(path string) string {
	f, e := os.Open(path)
	if e != nil {
		return ""
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return ""
	}
	start := info.Size() - 8192
	if start < 0 {
		start = 0
	}
	b := make([]byte, info.Size()-start)
	n, _ := f.ReadAt(b, start)
	return string(b[:n])
}
func (a *Agent) processResult(spec runnerSpec, result runnerExit) (json.RawMessage, error) {
	duration := int64(0)
	if !result.StartedAt.IsZero() {
		duration = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	}
	return marshalResult(map[string]any{"exit_code": result.ExitCode, "duration_ms": duration, "stdout_summary": processSummary(filepath.Join(spec.Dir, "stdout.log")), "stderr_summary": processSummary(filepath.Join(spec.Dir, "stderr.log")), "started_at": result.StartedAt, "finished_at": result.FinishedAt})
}
func processError(result runnerExit) error {
	if result.ErrorCode == "" && result.ExitCode == 0 {
		return nil
	}
	code := result.ErrorCode
	if code == "" {
		code = "PROCESS_EXIT_NONZERO"
	}
	return protocol.Err(code, fmt.Sprintf("exit_code=%d: %s", result.ExitCode, result.Message))
}

func (a *Agent) logsDrained(t protocol.TaskEnvelope, dir string) bool {
	for _, stream := range []string{"stdout", "stderr"} {
		info, e := os.Stat(filepath.Join(dir, stream+".log"))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false
		}
		a.mu.Lock()
		var offset int64
		e = a.db.QueryRow(`SELECT `+stream+`_offset FROM local_task_ledger WHERE id=?`, t.TaskID).Scan(&offset)
		a.mu.Unlock()
		if e != nil || offset < info.Size() {
			return false
		}
	}
	return true
}

func (a *Agent) processProgress(t protocol.TaskEnvelope) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = appendEvent(tx, a.cfg.DeviceID, t, "task.progress", protocol.EventPayload{Message: "process finished", Completed: 1, Total: 1}, "running"); e != nil {
		return e
	}
	return tx.Commit()
}
