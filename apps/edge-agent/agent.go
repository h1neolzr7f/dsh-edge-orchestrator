// Package agent executes authorized built-ins with durable receipts and an event outbox.
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Executor func(context.Context, protocol.TaskEnvelope) (json.RawMessage, error)
type Config struct {
	DBPath, DeviceID, Token, CoordinatorURL string
	Workspaces                              map[string]string
	AllowedPrograms                         []string
	TerminationGrace                        time.Duration
	HTTPClient                              *http.Client
	Executor                                Executor
}
type Agent struct {
	cfg             Config
	dataDir         string
	programs        map[string]string
	resuming        map[string]bool
	db              *sql.DB
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	conn            *edgelink.Conn
	active          map[string]context.CancelFunc
	cancelled       map[string]bool
	wg              sync.WaitGroup
	runWG           sync.WaitGroup
	closed, running bool
	closeOnce       sync.Once
	lastError       error
}

func Open(cfg Config) (*Agent, error) {
	if cfg.DBPath == "" || !protocol.ValidID(cfg.DeviceID) || cfg.Token == "" || cfg.CoordinatorURL == "" {
		return nil, fmt.Errorf("database, device ID, token and coordinator URL required")
	}
	if cfg.DBPath != ":memory:" {
		if e := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); e != nil {
			return nil, e
		}
	}
	dataDir, programs, e := configureLocal(&cfg)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", cfg.DBPath)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	if e = ensureProcessColumns(db); e != nil {
		db.Close()
		return nil, e
	}
	var id string
	e = db.QueryRow(`SELECT id FROM identity LIMIT 1`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = db.Exec(`INSERT INTO identity(id) VALUES(?)`, cfg.DeviceID)
	} else if e == nil && id != cfg.DeviceID {
		e = fmt.Errorf("database belongs to device %s", id)
	}
	if e != nil {
		db.Close()
		return nil, e
	}
	if cfg.DBPath != ":memory:" {
		if e = os.Chmod(cfg.DBPath, 0600); e != nil {
			db.Close()
			return nil, e
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{cfg: cfg, dataDir: dataDir, programs: programs, resuming: map[string]bool{}, db: db, ctx: ctx, cancel: cancel, active: map[string]context.CancelFunc{}, cancelled: map[string]bool{}}
	rows, err := db.Query(`SELECT task_id FROM cancellations`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		a.cancelled[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	return a, nil
}
func (a *Agent) Close() error {
	var e error
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		if a.conn != nil {
			a.conn.Close()
		}
		a.mu.Unlock()
		a.runWG.Wait()
		a.wg.Wait()
		e = a.db.Close()
	})
	return e
}

// Reconnect responds to an OS network-change notification without cancelling tasks.
func (a *Agent) Reconnect() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		a.conn.Close()
	}
}
func (a *Agent) LastError() error { a.mu.Lock(); defer a.mu.Unlock(); return a.lastError }
func (a *Agent) Run(ctx context.Context) error {
	a.mu.Lock()
	if a.closed || a.running {
		a.mu.Unlock()
		return fmt.Errorf("agent already running or closed")
	}
	a.running = true
	a.runWG.Add(1)
	a.mu.Unlock()
	defer a.runWG.Done()
	defer a.cancel()
	stop := context.AfterFunc(ctx, a.cancel)
	defer stop()
	accepted, e := a.recoverTasks()
	if e != nil {
		return e
	}
	for _, t := range accepted {
		a.launch(t)
	}
	backoff := 50 * time.Millisecond
	for {
		if a.ctx.Err() != nil {
			return nil
		}
		e = a.connect()
		a.mu.Lock()
		a.lastError = e
		a.mu.Unlock()
		var pe *protocol.Error
		if errors.As(e, &pe) && (pe.Code == "EVENT_HISTORY_LOST" || pe.Code == "INVALID_EVENT_ACK") {
			return e
		}
		delay := time.Duration(rand.Int63n(int64(backoff) + 1))
		timer := time.NewTimer(delay)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 3*time.Second {
			backoff *= 2
			if backoff > 3*time.Second {
				backoff = 3 * time.Second
			}
		}
	}
}
func (a *Agent) Registration() protocol.DeviceRegistration {
	caps := []protocol.CapabilityManifest{}
	for _, id := range []string{"device.inspect", "report.aggregate", "process.exec", "process.cancel", "filesystem.read", "filesystem.write", "git.inspect"} {
		caps = append(caps, protocol.CapabilityManifest{CapabilityID: id, Version: "1.0.0", Description: "Authorized local capability", InputSchema: json.RawMessage(capabilitySchemas()[id]), OutputSchema: json.RawMessage(`{"type":"object"}`), PermissionScope: id, ResourceClass: "cpu", MaxConcurrency: 4, Discovery: "static", Authorized: true})
	}
	return protocol.DeviceRegistration{Manifest: protocol.DeviceManifest{DeviceID: a.cfg.DeviceID, DisplayName: a.cfg.DeviceID, Platform: protocol.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Version: hostVersion()}, AgentVersion: "0.2.0", Labels: []string{}, Resources: hostResources(filepath.Dir(a.cfg.DBPath)), CapabilitiesRevision: 2}, Capabilities: caps}
}
func (a *Agent) connect() error {
	c, e := edgelink.Dial(a.ctx, a.cfg.CoordinatorURL+"/v1/link", a.cfg.Token, a.cfg.HTTPClient)
	if e != nil {
		return e
	}
	a.mu.Lock()
	a.conn = c
	a.mu.Unlock()
	defer func() {
		c.Close()
		a.mu.Lock()
		if a.conn == c {
			a.conn = nil
		}
		a.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	send := func(kind, id string, v any) error {
		x, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		return c.Send(x, kind, id, v)
	}
	receive := func() (protocol.Envelope, error) {
		x, done := context.WithTimeout(ctx, 45*time.Second)
		defer done()
		return c.Receive(x)
	}
	last, e := a.lastAck()
	if e != nil {
		return e
	}
	if e = send("client.hello", a.cfg.DeviceID, protocol.ClientHello{Role: "worker", DeviceID: a.cfg.DeviceID, AgentVersion: "0.2.0", ProtocolVersions: []string{protocol.Version}, LastWorkerSeq: last, PendingMessageIDs: []string{}}); e != nil {
		return e
	}
	m, e := receive()
	if e != nil {
		return e
	}
	if m.Kind == "link.error" {
		var pe protocol.Error
		if e = protocol.Decode(m.Payload, &pe); e != nil {
			return e
		}
		return &pe
	}
	if m.Kind != "server.welcome" {
		return fmt.Errorf("expected server.welcome")
	}
	var welcome protocol.ServerWelcome
	if e = protocol.Decode(m.Payload, &welcome); e != nil {
		return e
	}
	if welcome.SelectedProtocol != protocol.Version {
		return fmt.Errorf("protocol mismatch")
	}
	if e = a.ack(welcome.Resume.LastWorkerSeq); e != nil {
		return e
	}
	if e = send("device.register", a.cfg.DeviceID, a.Registration()); e != nil {
		return e
	}
	m, e = receive()
	if e != nil {
		return e
	}
	if m.Kind != "device.registered" {
		return fmt.Errorf("expected device.registered")
	}
	if e = send("link.ready", a.cfg.DeviceID, map[string]bool{"ready": true}); e != nil {
		return e
	}
	var pendingAck atomic.Int64
	pendingAck.Store(welcome.Resume.LastWorkerSeq)
	pumpError := make(chan error, 1)
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		sent := welcome.Resume.LastWorkerSeq
		persistedAck := sent
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				if send("device.heartbeat", a.cfg.DeviceID, map[string]bool{"alive": true}) != nil {
					c.Close()
					return
				}
			case <-ticker.C:
				latest := pendingAck.Load()
				if latest > persistedAck {
					if err := a.ack(latest); err != nil {
						pumpError <- err
						c.Close()
						return
					}
					persistedAck = latest
				}
				events, e := a.outbox(sent)
				if e != nil {
					c.Close()
					return
				}
				for _, ev := range events {
					if ev.WorkerSeq <= sent {
						continue
					}
					if send("event.append", ev.TaskID, ev) != nil {
						c.Close()
						return
					}
					sent = ev.WorkerSeq
				}
			}
		}
	}()
	defer func() { cancel(); c.Close(); <-pumpDone }()
	for {
		m, e = receive()
		if e != nil {
			select {
			case cause := <-pumpError:
				return cause
			default:
				return e
			}
		}
		switch m.Kind {
		case "task.submit":
			var t protocol.TaskEnvelope
			if e = protocol.Decode(m.Payload, &t); e != nil {
				return e
			}
			receipt, _, e := a.Accept(t)
			if e != nil {
				return e
			}
			if e = send("task.accepted", t.TaskID, receipt); e != nil {
				return e
			}
			a.launch(t)
		case "task.cancel":
			var req protocol.TaskRequest
			if e = protocol.Decode(m.Payload, &req); e != nil {
				return e
			}
			if e = a.cancelTask(req.TaskID); e != nil {
				return e
			}
		case "task.reconcile":
			var req protocol.TaskRequest
			if e = protocol.Decode(m.Payload, &req); e != nil {
				return e
			}
			snap, e := a.taskSnapshot(req.TaskID)
			if e != nil {
				return e
			}
			if e = send("task.snapshot", req.TaskID, snap); e != nil {
				return e
			}
		case "event.ack":
			var ack protocol.EventAck
			if e = protocol.Decode(m.Payload, &ack); e != nil {
				return e
			}
			if ack.LastWorkerSeq < pendingAck.Load() {
				return protocol.Err("EVENT_HISTORY_LOST", "coordinator acknowledgment went backwards")
			}
			if ack.LastWorkerSeq > protocol.MaxSequence {
				return protocol.Err("INVALID_EVENT_ACK", "ack exceeds protocol limit")
			}
			pendingAck.Store(ack.LastWorkerSeq)
		case "link.pong":
		case "link.error":
			var pe protocol.Error
			if e = protocol.Decode(m.Payload, &pe); e != nil {
				return e
			}
			return &pe
		default:
			return fmt.Errorf("unsupported message %s", m.Kind)
		}
	}
}
func (a *Agent) launch(t protocol.TaskEnvelope) {
	a.mu.Lock()
	if a.closed || a.active[t.TaskID] != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	if t.Deadline != nil {
		cancel()
		ctx, cancel = context.WithDeadline(a.ctx, *t.Deadline)
	}
	a.active[t.TaskID] = cancel
	if a.cancelled[t.TaskID] {
		cancel()
	}
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() { a.mu.Lock(); delete(a.resuming, t.TaskID); delete(a.active, t.TaskID); a.mu.Unlock() }()
		claimed, e := a.claim(t)
		if e != nil {
			a.setError(e)
			return
		}
		a.mu.Lock()
		resuming := a.resuming[t.TaskID]
		a.mu.Unlock()
		if !claimed && !resuming {
			return
		}
		var result json.RawMessage
		var runErr error
		prepared, prepErr := ctx, error(nil)
		if !resuming {
			prepared, prepErr = a.prepareTask(ctx, t)
		}
		if prepErr != nil {
			runErr = prepErr
		} else if len(t.InputArtifacts) > 0 || len(t.InputArtifactBindings) > 0 {
			runErr = protocol.Err("UNSUPPORTED_PRECONDITION", "workspace/artifact executors not installed")
		} else if !supports(t) {
			runErr = protocol.Err("UNSUPPORTED_CAPABILITY", t.Capability.ID)
		} else if ctx.Err() != nil && !resuming {
			runErr = ctx.Err()
		} else if a.cfg.Executor != nil {
			result, runErr = a.cfg.Executor(prepared, t)
		} else {
			result, runErr = a.execute(prepared, t)
		}
		if errors.Is(runErr, errAgentDetached) {
			return
		}
		if runErr == nil && result != nil && !json.Valid(result) {
			runErr = protocol.Err("INVALID_RESULT", "executor returned invalid JSON")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			runErr = protocol.Err("DEADLINE_EXCEEDED", "task deadline exceeded")
		}
		a.mu.Lock()
		cancelled := a.cancelled[t.TaskID]
		a.mu.Unlock()
		if a.ctx.Err() != nil && !cancelled {
			runErr = protocol.Err("interrupted_by_restart", "agent stopped before durable completion")
		}
		if e = a.finish(t, result, runErr, cancelled); e != nil {
			a.setError(e)
			return
		}
		// Process executors have drained their logs before returning. Future
		// output uploads must also complete before this cleanup boundary.
		if e = a.cleanupTaskWorkspace(t); e != nil {
			a.setError(e)
		}
	}()
}
func (a *Agent) setError(e error) { a.mu.Lock(); a.lastError = e; a.mu.Unlock() }
func supports(t protocol.TaskEnvelope) bool {
	_, ok := capabilitySchemas()[t.Capability.ID]
	return ok && protocol.Matches(t.Capability.Version, "1.0.0")
}
func (a *Agent) execute(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		a.mu.Lock()
		resuming := a.resuming[t.TaskID]
		a.mu.Unlock()
		// A recovered process still needs its monitor to stop the owned tree
		// and drain logs even when cancellation was persisted before restart.
		if t.Capability.ID != "process.exec" || !resuming {
			return nil, e
		}
	}
	switch t.Capability.ID {
	case "device.inspect":
		b, e := json.Marshal(a.Registration().Manifest)
		return b, e
	case "report.aggregate":
		var input struct {
			Results []struct {
				TaskID string          `json:"task_id"`
				Result json.RawMessage `json:"result"`
			} `json:"results"`
		}
		if e := protocol.Decode(t.Input, &input); e != nil {
			return nil, e
		}
		b, e := json.Marshal(map[string]any{"reports": input.Results, "count": len(input.Results)})
		return b, e
	}
	return a.executeLocal(ctx, t)
}
