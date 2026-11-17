package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Secure existing sidecars before opening SQLite; newly created sidecars inherit
// the restrictive database mode. Never follow a symlink to a credential file.
func secureIdentityFiles(path string) error {
	if path == ":memory:" {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		p := path + suffix
		st, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			if suffix != "" {
				continue
			}
			f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if e != nil {
				return e
			}
			if e = f.Close(); e != nil {
				return e
			}
			continue
		}
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("credential database must be a regular file")
		}
		if e = os.Chmod(p, 0600); e != nil {
			return e
		}
	}
	return nil
}
func identityEndpoint(endpoint string) (string, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("valid HTTPS coordinator URL required")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func loadIdentity(db *sql.DB, cfg *Config) error {
	endpoint, e := identityEndpoint(cfg.CoordinatorURL)
	if e != nil {
		return e
	}
	cfg.CoordinatorURL = endpoint
	// Additive migration preserves existing static-mode worker ledgers.
	rows, e := db.Query(`PRAGMA table_info(identity)`)
	if e != nil {
		return e
	}
	cols := map[string]bool{}
	for rows.Next() {
		var n, notnull, pk int
		var name, typ string
		var def any
		if e = rows.Scan(&n, &name, &typ, &notnull, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		cols[name] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, name := range []string{"token", "endpoint"} {
		if !cols[name] {
			if _, e = db.Exec(`ALTER TABLE identity ADD COLUMN ` + name + ` TEXT`); e != nil {
				return e
			}
		}
	}
	var id string
	var token, savedEndpoint sql.NullString
	e = db.QueryRow(`SELECT id,token,endpoint FROM identity LIMIT 1`).Scan(&id, &token, &savedEndpoint)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if id != "" {
		if cfg.DeviceID == "" {
			cfg.DeviceID = id
		}
		if cfg.DeviceID != id {
			return fmt.Errorf("database belongs to a different device")
		}
	}
	if !protocol.ValidID(cfg.DeviceID) {
		return fmt.Errorf("valid device ID required on first startup")
	}
	if cfg.PairingCode != "" && cfg.Token != "" {
		return fmt.Errorf("choose pairing or static worker token")
	}
	if token.String != "" {
		if savedEndpoint.String != endpoint {
			return fmt.Errorf("saved credential belongs to a different coordinator endpoint")
		}
		if cfg.PairingCode != "" {
			return fmt.Errorf("device already paired")
		}
		if cfg.Token == "" {
			cfg.Token = token.String
		}
	}
	if cfg.PairingCode != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// A pairing credential must never cross a redirect to another origin.
		client := edgelink.NewClient(endpoint, "", cfg.HTTPClient)
		copyHTTP := *client.HTTP
		copyHTTP.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("pairing redirects forbidden") }
		client.HTTP = &copyHTTP
		out, err := client.ClaimPairing(ctx, protocol.PairingClaim{PairingCode: cfg.PairingCode, DeviceID: cfg.DeviceID, DisplayName: cfg.DeviceID})
		if err != nil {
			return err
		}
		if out.DeviceID != cfg.DeviceID || out.WorkerToken == "" {
			return fmt.Errorf("invalid pairing credential response")
		}
		cfg.Token = out.WorkerToken
		if e = secureIdentityFiles(cfg.DBPath); e != nil {
			return e
		}
		_, e = db.Exec(`INSERT INTO identity(id,token,endpoint) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET token=excluded.token,endpoint=excluded.endpoint`, cfg.DeviceID, cfg.Token, endpoint)
		return e
	}
	if cfg.Token == "" {
		return fmt.Errorf("worker token or one-time pairing code required")
	}
	if id == "" {
		_, e = db.Exec(`INSERT INTO identity(id) VALUES(?)`, cfg.DeviceID)
	}
	return e
}
