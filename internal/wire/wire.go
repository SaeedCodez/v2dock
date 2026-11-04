// Package wire holds the constants shared by the agent, the web gateway and
// the CLI.
package wire

const (
	// HeaderGateway is set (and overwritten) by the web gateway on every
	// request it forwards. The agent treats requests that carry it as coming
	// from the network and requires authentication; requests without it come
	// from a local process with access to the agent's socket (root / the CLI).
	HeaderGateway = "X-V2dock-Gateway"
	// HeaderClientIP is the client address as resolved by the gateway.
	HeaderClientIP = "X-V2dock-Client-Ip"
	// HeaderProto is "https" when the browser connection is TLS.
	HeaderProto = "X-V2dock-Proto"
	// HeaderCSRF must accompany cookie-authenticated state changes. Browsers
	// cannot add custom headers to cross-site requests without a CORS
	// preflight, which the gateway never approves.
	HeaderCSRF = "X-V2dock-Csrf"

	DefaultAgentSocket = "/run/v2dock/agent/agent.sock"
	DefaultGuardSocket = "/run/v2dock/docker/docker.sock"
)
