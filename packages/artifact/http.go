package artifact

import (
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
)

// CredentialHTTP copies the caller's transport and deadlines but never redirects
// authenticated requests or upload/claim bodies to a different endpoint.
func CredentialHTTP(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return protocol.Err("REDIRECT_FORBIDDEN", "fleet credentialed requests must not redirect")
	}
	return &copyClient
}
