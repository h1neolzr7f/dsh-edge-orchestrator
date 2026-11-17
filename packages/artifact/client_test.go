package artifact

import (
	"context"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadInvalidEndpointReturnsConfigurationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, e := NewClient("http://localhost", "token", nil).UploadFile(ctx, protocol.ArtifactUploadRequest{}, path)
	if e == nil || !strings.Contains(e.Error(), "HTTPS coordinator required") {
		t.Fatalf("invalid endpoint retried instead of returning configuration error: %v", e)
	}
}
