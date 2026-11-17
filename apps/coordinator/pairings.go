package coordinator

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"time"
)

func ioReadAuthority(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
}
func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func tokenHash(token string) string {
	v := sha256.Sum256([]byte(token))
	return hex.EncodeToString(v[:])
}
func (s *Server) workerDevice(token string) string {
	if token == "" {
		return ""
	}
	for id, t := range s.cfg.WorkerTokens {
		if sameToken(token, t) {
			return id
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	if s.db.QueryRow(`SELECT device_id FROM device_credentials WHERE token_hash=?`, tokenHash(token)).Scan(&id) != nil {
		return ""
	}
	return id
}
func (s *Server) CreatePairing(in protocol.PairingRequest) (protocol.Pairing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := protocol.Pairing{CoordinatorURL: in.CoordinatorURL, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if (in.DeviceHint != "" && !protocol.ValidID(in.DeviceHint)) || (in.RecoverExisting && in.DeviceHint == "") {
		return out, protocol.Err("INVALID_PAYLOAD", "invalid device hint")
	}
	if in.RecoverExisting {
		var registered int
		if e := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=?`, in.DeviceHint).Scan(&registered); e != nil {
			return out, e
		}
		if registered != 0 {
			return out, protocol.Err("DEVICE_ENROLLED", "restore the existing Worker ledger or use a new device ID")
		}
	}
	code, e := randomSecret(8)
	if e != nil {
		return out, e
	}
	out.PairingCode = code
	_, e = s.db.Exec(`INSERT INTO pairings(code,device_hint,expires_at,recover_existing) VALUES(?,?,?,?)`, out.PairingCode, in.DeviceHint, out.ExpiresAt.Unix(), in.RecoverExisting)
	return out, e
}
func (s *Server) ClaimPairing(in protocol.PairingClaim) (protocol.PairingCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out protocol.PairingCredential
	if !protocol.ValidID(in.DeviceID) || in.PairingCode == "" {
		return out, protocol.Err("INVALID_PAYLOAD", "device ID and pairing code required")
	}
	if _, ok := s.cfg.WorkerTokens[in.DeviceID]; ok {
		return out, protocol.Err("DEVICE_EXISTS", "static device already configured")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	result, e := tx.Exec(`UPDATE pairings SET used=1 WHERE code=? AND used=0 AND expires_at>? AND (device_hint='' OR device_hint=?)`, in.PairingCode, time.Now().Unix(), in.DeviceID)
	if e != nil {
		return out, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return out, e
	}
	if n != 1 {
		return out, protocol.Err("PAIRING_UNAVAILABLE", "invalid, expired, consumed or mismatched pairing code")
	}
	token, e := randomSecret(32)
	if e != nil {
		return out, e
	}
	var recoverExisting bool
	if e = tx.QueryRow(`SELECT recover_existing FROM pairings WHERE code=?`, in.PairingCode).Scan(&recoverExisting); e != nil {
		return out, e
	}
	if recoverExisting {
		var registered int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=?`, in.DeviceID).Scan(&registered); e != nil {
			return out, e
		}
		if registered != 0 {
			return out, protocol.Err("DEVICE_ENROLLED", "device registered after recovery code creation; preserve its existing ledger")
		}
	}
	insert := `INSERT INTO device_credentials(device_id,token_hash,created_at) VALUES(?,?,?)`
	if recoverExisting {
		insert += ` ON CONFLICT(device_id) DO UPDATE SET token_hash=excluded.token_hash,created_at=excluded.created_at`
	}
	_, e = tx.Exec(insert, in.DeviceID, tokenHash(token), time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return out, protocol.Err("DEVICE_EXISTS", "device credential already exists")
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	if recoverExisting {
		if session := s.sessions[in.DeviceID]; session != nil {
			session.conn.Close()
		}
	}
	return protocol.PairingCredential{DeviceID: in.DeviceID, WorkerToken: token}, nil
}
func (s *Server) claimPairingHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var in protocol.PairingClaim
	if e := decodeAuthority(w, r, &in); e != nil {
		writeError(w, e)
		return
	}
	out, e := s.ClaimPairing(in)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, out)
}
