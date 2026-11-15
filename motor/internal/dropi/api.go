package dropi

import "context"

// API is what the Motor needs from Dropi. Two implementations exist: the REST
// client with an integration token, and the MCP client with an OAuth login.
type API interface {
	Ping(ctx context.Context) (int, error)
	Kind() string // "token" | "mcp"
	Quote(ctx context.Context, in QuoteInput) ([]Quote, error)
	Product(ctx context.Context, id string) (Product, error)
	CreateOrder(ctx context.Context, payload map[string]any) (OrderResult, error)
}

var _ API = (*Client)(nil)

// Kind says this is the integration-token client.
func (c *Client) Kind() string { return "token" }

// Kind says this is the OAuth/MCP client.
func (m *MCPAPI) Kind() string { return "mcp" }
