package edgelink

import (
	"context"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/artifact"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
)

func (c *Client) artifactClient() *artifact.Client { return artifact.NewClient(c.URL, c.Token, c.HTTP) }
func (c *Client) CreateArtifactUpload(ctx context.Context, in protocol.ArtifactUploadRequest) (protocol.ArtifactUploadSession, error) {
	return c.artifactClient().CreateUpload(ctx, in)
}
func (c *Client) UploadArtifactChunk(ctx context.Context, id string, offset int64, b []byte) (protocol.ArtifactUploadSession, error) {
	return c.artifactClient().UploadChunk(ctx, id, offset, b)
}
func (c *Client) CompleteArtifactUpload(ctx context.Context, id string) (protocol.ArtifactRef, error) {
	return c.artifactClient().CompleteUpload(ctx, id)
}
func (c *Client) UploadArtifactFile(ctx context.Context, in protocol.ArtifactUploadRequest, path string) (protocol.ArtifactRef, error) {
	return c.artifactClient().UploadFile(ctx, in, path)
}
func (c *Client) DownloadArtifactFile(ctx context.Context, id, path, sha string) error {
	return c.artifactClient().DownloadFile(ctx, id, path, sha)
}

// UploadArtifactOpenFile transfers an already confined regular file descriptor.
// The caller retains ownership of f.
func (c *Client) UploadArtifactOpenFile(ctx context.Context, in protocol.ArtifactUploadRequest, f *os.File) (protocol.ArtifactRef, error) {
	return c.artifactClient().UploadOpenFile(ctx, in, f)
}
