// Package artifact provides authenticated HTTPS artifact transfer with durable resume.
package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const ChunkSize = 4 << 20

var errHTTPSRequired = errors.New("HTTPS coordinator required")

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func NewClient(endpoint, token string, h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{strings.TrimRight(endpoint, "/"), token, h}
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errHTTPSRequired
	}
	r, e := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	return CredentialHTTP(c.HTTP).Do(r)
}
func responseJSON(res *http.Response, out any) error {
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if e != nil {
		return e
	}
	if res.StatusCode >= 300 {
		var pe protocol.Error
		if json.Unmarshal(b, &pe) == nil && pe.Code != "" {
			return &pe
		}
		return fmt.Errorf("artifact HTTP %d", res.StatusCode)
	}
	return json.Unmarshal(b, out)
}
func (c *Client) CreateUpload(ctx context.Context, in protocol.ArtifactUploadRequest) (protocol.ArtifactUploadSession, error) {
	var out protocol.ArtifactUploadSession
	b, e := json.Marshal(in)
	if e != nil {
		return out, e
	}
	res, e := c.request(ctx, "POST", "/v1/artifacts/uploads", bytes.NewReader(b))
	if e == nil {
		e = responseJSON(res, &out)
	}
	return out, e
}
func (c *Client) UploadChunk(ctx context.Context, id string, offset int64, b []byte) (protocol.ArtifactUploadSession, error) {
	var out protocol.ArtifactUploadSession
	res, e := c.request(ctx, "PATCH", "/v1/artifacts/uploads/"+url.PathEscape(id)+"?offset="+strconv.FormatInt(offset, 10), bytes.NewReader(b))
	if e == nil {
		e = responseJSON(res, &out)
	}
	return out, e
}
func (c *Client) CompleteUpload(ctx context.Context, id string) (protocol.ArtifactRef, error) {
	var out protocol.ArtifactRef
	res, e := c.request(ctx, "POST", "/v1/artifacts/uploads/"+url.PathEscape(id)+"/complete", nil)
	if e == nil {
		e = responseJSON(res, &out)
	}
	return out, e
}
func retry(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}
func permanent(e error) bool {
	var pe *protocol.Error
	return errors.Is(e, errHTTPSRequired) || errors.As(e, &pe) && pe.Code != "OFFSET_MISMATCH" && pe.Code != "INTERNAL_ERROR" && pe.Code != "STORE_ERROR"
}

// UploadFile hashes a regular file and resumes from the server-confirmed offset.
// Transport failures retry until ctx expires; semantic errors return immediately.
func (c *Client) UploadFile(ctx context.Context, in protocol.ArtifactUploadRequest, path string) (protocol.ArtifactRef, error) {
	var out protocol.ArtifactRef
	f, e := openRegular(path)
	if e != nil {
		return out, e
	}
	defer f.Close()
	return c.UploadOpenFile(ctx, in, f)
}

// UploadOpenFile uploads the caller's already checked descriptor without opening
// its pathname again. The caller retains ownership and must close the file.
// The descriptor must be seekable and regular; hashing starts at offset zero.
func (c *Client) UploadOpenFile(ctx context.Context, in protocol.ArtifactUploadRequest, f *os.File) (protocol.ArtifactRef, error) {
	var out protocol.ArtifactRef
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return out, e
	}
	st, e := f.Stat()
	if e != nil {
		return out, e
	}
	if !st.Mode().IsRegular() {
		return out, fmt.Errorf("artifact must be a regular file")
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return out, e
	}
	in.SizeBytes = st.Size()
	in.SHA256 = hex.EncodeToString(h.Sum(nil))
	buf := make([]byte, ChunkSize)
	for {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		u, e := c.CreateUpload(ctx, in)
		if e != nil {
			if permanent(e) {
				return out, e
			}
			if e = retry(ctx); e != nil {
				return out, e
			}
			continue
		}
		if u.ConfirmedOffset < 0 || u.ConfirmedOffset > in.SizeBytes {
			return out, fmt.Errorf("invalid server offset")
		}
		offset := u.ConfirmedOffset
		for offset < in.SizeBytes {
			n := int64(ChunkSize)
			if in.SizeBytes-offset < n {
				n = in.SizeBytes - offset
			}
			if _, e = f.ReadAt(buf[:n], offset); e != nil {
				return out, e
			}
			var next protocol.ArtifactUploadSession
			next, e = c.UploadChunk(ctx, u.UploadID, offset, buf[:n])
			if e != nil {
				break
			}
			if next.ConfirmedOffset != offset+n {
				return out, fmt.Errorf("invalid chunk acknowledgment")
			}
			offset = next.ConfirmedOffset
		}
		if e == nil {
			out, e = c.CompleteUpload(ctx, u.UploadID)
			if e == nil {
				return out, nil
			}
		}
		if permanent(e) {
			return out, e
		}
		if e = retry(ctx); e != nil {
			return out, e
		}
	}
}

// DownloadFile preserves partial bytes at path across interruption/restart and
// validates the final SHA-256. Empty expectedSHA uses the ready artifact ETag.
func (c *Client) DownloadFile(ctx context.Context, id, path, expectedSHA string) error {
	forceFull := false
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e != nil {
			f.Close()
			return e
		}
		have := st.Size()
		u, e := url.Parse(c.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" {
			f.Close()
			return errHTTPSRequired
		}
		r, e := http.NewRequestWithContext(ctx, "GET", c.URL+"/v1/artifacts/"+url.PathEscape(id), nil)
		if e != nil {
			f.Close()
			return e
		}
		r.Header.Set("Authorization", "Bearer "+c.Token)
		if have > 0 && !forceFull {
			r.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
		}
		res, e := CredentialHTTP(c.HTTP).Do(r)
		if e != nil {
			f.Close()
			if permanent(e) {
				return e
			}
			if e = retry(ctx); e != nil {
				return e
			}
			continue
		}
		if res.StatusCode >= 500 {
			res.Body.Close()
			f.Close()
			if e = retry(ctx); e != nil {
				return e
			}
			continue
		}
		if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
			f.Close()
			return responseJSON(res, &struct{}{})
		}
		if res.StatusCode == 416 && expectedSHA == "" && !forceFull {
			// Go ServeContent may strip ETag on 416. Re-fetch successfully from zero
			// to obtain an authenticated digest; never trust the local bytes alone.
			res.Body.Close()
			f.Close()
			forceFull = true
			continue
		}
		hash := strings.Trim(res.Header.Get("ETag"), `"`)
		if expectedSHA != "" {
			hash = expectedSHA
		}
		raw, he := hex.DecodeString(hash)
		if he != nil || len(raw) != 32 {
			res.Body.Close()
			f.Close()
			return protocol.Err("ARTIFACT_HASH_MISMATCH", "missing or invalid artifact digest")
		}
		forceFull = false
		if res.StatusCode == 416 && have > 0 {
			res.Body.Close()
			e = verify(f, hash)
			f.Close()
			return e
		}
		if res.StatusCode != 200 && res.StatusCode != 206 {
			f.Close()
			return responseJSON(res, &struct{}{})
		}
		if res.StatusCode == 200 {
			have = 0
			if e = f.Truncate(0); e != nil {
				res.Body.Close()
				f.Close()
				return e
			}
		} else {
			var start, end, total int64
			if _, e = fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); e != nil || start != have || end < start || total <= end {
				res.Body.Close()
				f.Close()
				return fmt.Errorf("invalid artifact Content-Range")
			}
		}
		_, e = f.Seek(have, io.SeekStart)
		if e == nil {
			_, e = io.Copy(f, res.Body)
		}
		res.Body.Close()
		syncErr := f.Sync()
		if e == nil {
			e = syncErr
		}
		if e == nil {
			e = verify(f, hash)
			f.Close()
			return e
		}
		f.Close()
		if e = retry(ctx); e != nil {
			return e
		}
	}
}
func verify(f *os.File, want string) error {
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	h := sha256.New()
	if _, e := io.Copy(h, f); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return protocol.Err("ARTIFACT_HASH_MISMATCH", "download digest differs")
	}
	return nil
}
