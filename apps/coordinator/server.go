// Package coordinator runs the persistent, controller-independent DAG coordinator.
package coordinator

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	DBPath          string
	ControllerToken string
	WorkerTokens    map[string]string
}
type Server struct {
	cfg       Config
	db        *sql.DB
	mu        sync.Mutex
	sessions  map[string]*session
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closing   bool
	lastError error
}
type wire struct {
	kind, id string
	payload  any
}
type session struct {
	conn  *edgelink.Conn
	queue chan wire
}

func Open(cfg Config) (*Server, error) {
	if cfg.DBPath == "" || cfg.ControllerToken == "" || len(cfg.WorkerTokens) == 0 {
		return nil, fmt.Errorf("database, controller token and per-device worker tokens required")
	}
	tokens := map[string]bool{cfg.ControllerToken: true}
	copyTokens := map[string]string{}
	for id, t := range cfg.WorkerTokens {
		if !protocol.ValidID(id) || t == "" || tokens[t] {
			return nil, fmt.Errorf("unique worker IDs/tokens required")
		}
		tokens[t] = true
		copyTokens[id] = t
	}
	cfg.WorkerTokens = copyTokens
	if cfg.DBPath != ":memory:" {
		if e := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); e != nil {
			return nil, e
		}
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
	if cfg.DBPath != ":memory:" {
		if e = os.Chmod(cfg.DBPath, 0600); e != nil {
			db.Close()
			return nil, e
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, db: db, sessions: map[string]*session{}, ctx: ctx, cancel: cancel}
	s.wg.Add(1)
	go s.scheduleLoop()
	return s, nil
}
func (s *Server) Close() error {
	var e error
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		s.closing = true
		for _, v := range s.sessions {
			v.conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
		e = s.db.Close()
	})
	return e
}
func sameToken(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		e := s.lastError
		s.mu.Unlock()
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/link", s.workerLink)
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		if !sameToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.cfg.ControllerToken) {
			writeJSON(w, 401, protocol.Error{Code: "UNAUTHORIZED", Message: "controller credential required"})
			return
		}
		s.controller(w, r)
	})
	return mux
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, e error) {
	var pe *protocol.Error
	if !errors.As(e, &pe) {
		pe = &protocol.Error{Code: "INTERNAL_ERROR", Message: "internal operation failed"}
	}
	status := 400
	switch pe.Code {
	case "PLAN_NOT_FOUND", "TASK_NOT_FOUND":
		status = 404
	case "IDEMPOTENCY_CONFLICT", "EVENT_CONFLICT", "PLAN_TERMINAL", "PLAN_CANCELLING":
		status = 409
	case "STORE_ERROR", "INTERNAL_ERROR":
		status = 500
	}
	writeJSON(w, status, pe)
}
func (s *Server) controller(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/plans" && r.Method == http.MethodPost:
		var p protocol.PlanEnvelope
		b := http.MaxBytesReader(w, r.Body, 2<<20)
		raw, e := io.ReadAll(b)
		if e == nil {
			e = protocol.Decode(raw, &p)
		}
		if e != nil {
			writeError(w, protocol.Err("INVALID_PAYLOAD", e.Error()))
			return
		}
		out, e := s.Submit(p)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 202, out)
	case r.URL.Path == "/v1/devices" && r.Method == http.MethodGet:
		out, e := s.Devices()
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, out)
	case r.URL.Path == "/v1/snapshot" && r.Method == http.MethodGet:
		out, e := s.Snapshot()
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, out)
	case r.URL.Path == "/v1/events" && r.Method == http.MethodGet:
		var vals [3]int64
		for i, key := range []string{"after_coord_seq", "high_watermark", "limit"} {
			v := r.URL.Query().Get(key)
			if v == "" {
				if key == "limit" {
					vals[i] = 500
				}
				continue
			}
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil {
				writeError(w, protocol.Err("INVALID_PAYLOAD", "invalid "+key))
				return
			}
			vals[i] = n
		}
		out, e := s.Events(vals[0], vals[1], int(vals[2]))
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, out)
	case strings.HasPrefix(r.URL.Path, "/v1/plans/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/plans/"), "/")
		var out protocol.PlanSnapshot
		var e error
		if len(parts) == 1 && r.Method == http.MethodGet {
			out, e = s.Plan(parts[0])
		} else if len(parts) == 2 && r.Method == http.MethodPost && (parts[1] == "cancel" || parts[1] == "pause" || parts[1] == "resume") {
			out, e = s.Control(parts[0], parts[1])
		} else {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, out)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) workerLink(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	expected := ""
	for id, t := range s.cfg.WorkerTokens {
		if sameToken(token, t) {
			expected = id
		}
	}
	if expected == "" {
		writeJSON(w, 401, protocol.Error{Code: "UNAUTHORIZED", Message: "worker credential required"})
		return
	}
	conn, e := edgelink.Accept(w, r)
	if e != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	receive := func() (protocol.Envelope, error) {
		c, done := context.WithTimeout(ctx, 45*time.Second)
		defer done()
		return conn.Receive(c)
	}
	send := func(kind, id string, v any) error {
		c, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		return conn.Send(c, kind, id, v)
	}
	msg, e := receive()
	if e != nil || msg.Kind != "client.hello" {
		return
	}
	var hello protocol.ClientHello
	if protocol.Decode(msg.Payload, &hello) != nil || hello.Validate() != nil || hello.Role != "worker" || hello.DeviceID != expected {
		return
	}
	supported := false
	for _, v := range hello.ProtocolVersions {
		if v == protocol.Version {
			supported = true
		}
	}
	if !supported {
		return
	}
	s.mu.Lock()
	var last int64
	e = s.db.QueryRow(`SELECT last_worker_seq FROM devices WHERE id=?`, expected).Scan(&last)
	s.mu.Unlock()
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return
	}
	if hello.LastWorkerSeq > last {
		_ = send("link.error", msg.MessageID, &protocol.Error{Code: "EVENT_HISTORY_LOST", Message: "coordinator history behind worker acknowledgment"})
		return
	}
	if e = send("server.welcome", msg.MessageID, protocol.ServerWelcome{ConnectionID: protocol.NewID("conn"), ServerTime: time.Now().UTC(), SelectedProtocol: protocol.Version, HeartbeatInterval: 20, Resume: protocol.Resume{LastWorkerSeq: last}}); e != nil {
		return
	}
	msg, e = receive()
	if e != nil || msg.Kind != "device.register" {
		return
	}
	var reg protocol.DeviceRegistration
	if e = protocol.Decode(msg.Payload, &reg); e != nil || reg.Manifest.DeviceID != expected || len(reg.Capabilities) == 0 {
		return
	}
	if !validRegistration(reg) {
		return
	}
	s.mu.Lock()
	_, e = s.db.Exec(`INSERT INTO devices(id,registration) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET registration=excluded.registration`, expected, encode(reg))
	s.mu.Unlock()
	if e != nil {
		return
	}
	if send("device.registered", msg.MessageID, map[string]string{"device_id": expected}) != nil {
		return
	}
	msg, e = receive()
	if e != nil || msg.Kind != "link.ready" {
		return
	}
	sess := &session{conn: conn, queue: make(chan wire, 64)}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	if old := s.sessions[expected]; old != nil {
		old.conn.Close()
	}
	s.sessions[expected] = sess
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.sessions[expected] == sess {
			delete(s.sessions, expected)
		}
		s.mu.Unlock()
		s.wg.Done()
	}()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-sess.queue:
				if send(m.kind, m.id, m.payload) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	defer func() { cancel(); conn.Close(); <-writerDone }()
	for {
		msg, e = receive()
		if e != nil {
			return
		}
		switch msg.Kind {
		case "event.append":
			var ev protocol.TaskEvent
			if e = protocol.Decode(msg.Payload, &ev); e != nil {
				return
			}
			ack, e := s.AppendEvent(expected, ev)
			if e != nil {
				_ = send("link.error", msg.MessageID, e)
				return
			}
			if e = send("event.ack", msg.MessageID, protocol.EventAck{LastWorkerSeq: ack}); e != nil {
				return
			}
		case "task.snapshot":
			var snap protocol.TaskReconcileSnapshot
			if protocol.Decode(msg.Payload, &snap) != nil || snap.DeviceID != expected {
				return
			}
			if snap.Error != nil && snap.Error.Code == "TASK_NOT_FOUND" {
				if e = s.resolveAbsentCancellation(expected, snap.TaskID); e != nil {
					return
				}
			}
		case "task.accepted":
			var rec protocol.TaskReceipt
			if protocol.Decode(msg.Payload, &rec) != nil || rec.Validate() != nil || rec.DeviceID != expected {
				return
			}
			s.mu.Lock()
			var raw string
			e = s.db.QueryRow(`SELECT payload FROM tasks WHERE id=?`, rec.TaskID).Scan(&raw)
			if e == nil {
				var task protocol.TaskEnvelope
				e = json.Unmarshal([]byte(raw), &task)
				if e == nil && task.TargetDeviceID == expected {
					_, e = s.db.Exec(`UPDATE tasks SET status='accepted' WHERE id=? AND status='dispatched'`, rec.TaskID)
				} else {
					e = fmt.Errorf("wrong task receipt")
				}
			}
			s.mu.Unlock()
			if e != nil {
				return
			}
		case "link.ping", "device.heartbeat":
			if send("link.pong", msg.MessageID, map[string]bool{"ok": true}) != nil {
				return
			}
		default:
			return
		}
	}
}
func validRegistration(r protocol.DeviceRegistration) bool {
	if !protocol.ValidID(r.Manifest.DeviceID) || r.Manifest.Platform.OS == "" || r.Manifest.Platform.Arch == "" {
		return false
	}
	seen := map[string]bool{}
	for _, c := range r.Capabilities {
		if c.CapabilityID == "" || seen[c.CapabilityID] || c.MaxConcurrency < 1 || !protocol.Matches("*", c.Version) || !json.Valid(c.InputSchema) || !json.Valid(c.OutputSchema) {
			return false
		}
		seen[c.CapabilityID] = true
	}
	return true
}
