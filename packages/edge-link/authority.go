package edgelink

import (
	"context"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/url"
)

func (c *Client) Approvals(ctx context.Context) ([]protocol.Approval, error) {
	out := []protocol.Approval{}
	e := c.request(ctx, "GET", "/v1/approvals", nil, &out)
	return out, e
}
func (c *Client) ResolveApproval(ctx context.Context, id string, d protocol.ApprovalDecision) (protocol.Approval, error) {
	var out protocol.Approval
	e := c.request(ctx, "POST", "/v1/approvals/"+url.PathEscape(id)+"/resolve", d, &out)
	return out, e
}
func (c *Client) CreatePairing(ctx context.Context, in protocol.PairingRequest) (protocol.Pairing, error) {
	var out protocol.Pairing
	e := c.request(ctx, "POST", "/v1/pairings", in, &out)
	return out, e
}
func (c *Client) ClaimPairing(ctx context.Context, in protocol.PairingClaim) (protocol.PairingCredential, error) {
	var out protocol.PairingCredential
	e := c.request(ctx, "POST", "/v1/pairings/claim", in, &out)
	return out, e
}
