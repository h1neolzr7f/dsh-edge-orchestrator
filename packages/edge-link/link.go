// Package edgelink implements connection-local framing over TLS WebSockets.
package edgelink

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/artifact"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Conn struct {
	ws       *websocket.Conn
	sendMu   sync.Mutex
	sent     atomic.Int64
	received atomic.Int64
}

func New(ws *websocket.Conn) *Conn { ws.SetReadLimit(2 << 20); return &Conn{ws: ws} }
func Dial(ctx context.Context, endpoint, token string, client *http.Client) (*Conn, error) {
	u, e := url.Parse(endpoint)
	if e != nil {
		return nil, e
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "wss":
	default:
		return nil, fmt.Errorf("TLS https/wss endpoint required")
	}
	ws, _, e := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: artifact.CredentialHTTP(client), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if e != nil {
		return nil, e
	}
	return New(ws), nil
}
func Accept(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	ws, e := websocket.Accept(w, r, nil)
	if e != nil {
		return nil, e
	}
	return New(ws), nil
}
func (c *Conn) Send(ctx context.Context, kind, correlation string, payload any) error {
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	m := protocol.Envelope{Protocol: protocol.Version, MessageID: protocol.NewID("msg"), Kind: kind, SentAt: time.Now().UTC(), CorrelationID: correlation, Sequence: c.sent.Add(1), Ack: c.received.Load(), Payload: b}
	b, e = json.Marshal(m)
	if e != nil {
		return e
	}
	return c.ws.Write(ctx, websocket.MessageText, b)
}
func (c *Conn) Receive(ctx context.Context) (protocol.Envelope, error) {
	var m protocol.Envelope
	typ, b, e := c.ws.Read(ctx)
	if e != nil {
		return m, e
	}
	if typ != websocket.MessageText {
		return m, fmt.Errorf("only JSON text frames allowed")
	}
	if e = protocol.Decode(b, &m); e != nil {
		return m, e
	}
	if e = m.Validate(); e != nil {
		return m, e
	}
	if m.Sequence != c.received.Load()+1 || m.Ack > c.sent.Load() {
		return m, fmt.Errorf("invalid connection sequence/ack")
	}
	c.received.Store(m.Sequence)
	return m, nil
}
func (c *Conn) Close() { _ = c.ws.CloseNow() }
func HTTPClient(caFile string) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		b, e := os.ReadFile(caFile)
		if e != nil {
			return nil, e
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, fmt.Errorf("invalid CA file")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}, nil
}
