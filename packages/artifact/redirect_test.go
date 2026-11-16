package artifact

import (
	"context"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialTransfersRejectRedirectsPermanently(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{}`))
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := NewClient(source.URL, "credential", source.Client())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := c.UploadChunk(ctx, "upload", 0, []byte("private chunk")); e == nil {
		t.Error("redirected chunk")
	}
	path := filepath.Join(t.TempDir(), "download")
	if e := c.DownloadFile(ctx, "artifact", path, ""); e == nil || ctx.Err() != nil {
		t.Errorf("redirect was retried or accepted: %v", e)
	}
	os.WriteFile(path, []byte("source"), 0600)
	if _, e := c.UploadFile(ctx, protocol.ArtifactUploadRequest{}, path); e == nil || ctx.Err() != nil {
		t.Errorf("upload redirect retried: %v", e)
	}
	if reached.Load() != 0 {
		t.Fatalf("redirect target received %d requests", reached.Load())
	}
}
