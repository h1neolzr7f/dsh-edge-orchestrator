package edgelink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func NewClient(endpoint, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{strings.TrimRight(endpoint, "/"), token, httpClient}
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	u, e := url.Parse(c.URL)
	if e != nil {
		return e
	}
	if u.Scheme != "https" {
		return fmt.Errorf("HTTPS coordinator required")
	}
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	res, e := c.HTTP.Do(r)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if e != nil {
		return e
	}
	if res.StatusCode >= 300 {
		var pe protocol.Error
		if json.Unmarshal(b, &pe) == nil && pe.Code != "" {
			return &pe
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.Unmarshal(b, output)
}
func (c *Client) Submit(ctx context.Context, p protocol.PlanEnvelope) (protocol.PlanAccepted, error) {
	var out protocol.PlanAccepted
	e := c.request(ctx, "POST", "/v1/plans", p, &out)
	return out, e
}
func (c *Client) Plan(ctx context.Context, id string) (protocol.PlanSnapshot, error) {
	var out protocol.PlanSnapshot
	e := c.request(ctx, "GET", "/v1/plans/"+url.PathEscape(id), nil, &out)
	return out, e
}
func (c *Client) Devices(ctx context.Context) ([]protocol.DeviceSnapshot, error) {
	var out []protocol.DeviceSnapshot
	e := c.request(ctx, "GET", "/v1/devices", nil, &out)
	return out, e
}
func (c *Client) Events(ctx context.Context, after, through int64, limit int) (protocol.EventPage, error) {
	var out protocol.EventPage
	q := url.Values{"after_coord_seq": []string{strconv.FormatInt(after, 10)}, "high_watermark": []string{strconv.FormatInt(through, 10)}, "limit": []string{strconv.Itoa(limit)}}
	e := c.request(ctx, "GET", "/v1/events?"+q.Encode(), nil, &out)
	return out, e
}
func (c *Client) Snapshot(ctx context.Context) (protocol.FleetSnapshot, error) {
	var out protocol.FleetSnapshot
	e := c.request(ctx, "GET", "/v1/snapshot", nil, &out)
	return out, e
}
func (c *Client) Control(ctx context.Context, id, action string) (protocol.PlanSnapshot, error) {
	var out protocol.PlanSnapshot
	if action != "cancel" && action != "pause" && action != "resume" {
		return out, fmt.Errorf("unknown plan action")
	}
	e := c.request(ctx, "POST", "/v1/plans/"+url.PathEscape(id)+"/"+action, nil, &out)
	return out, e
}
