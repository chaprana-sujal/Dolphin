package tui

import (
	"context"
	"net"
	"net/http"

	"github.com/moby/moby/client"
	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
)

// NewMultiplexedDockerClient creates a standard official Docker client
// that never touches the network card. Instead of dialing a unix socket or TCP,
// it dials a virtual Yamux stream and feeds the HTTP traffic directly into it.
func NewMultiplexedDockerClient(session *mux.Session) (*client.Client, error) {
	// 1. Create a custom http.Transport that intercepts DialContext.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Ignore the requested addr entirely! Just punch a hole into Yamux.
			return session.Open(mux.StreamTypeDocker)
		},
	}

	// 2. Create the standard http.Client wrapping our weird virtual transport.
	httpClient := &http.Client{
		Transport: transport,
	}

	// 3. Initialize the official Moby client with our injected HTTP client.
	// We pass "http://dolphin" to satisfy internal URL parsers, but the address
	// string is completely ignored by our transport.
	return client.NewClientWithOpts(
		client.WithHTTPClient(httpClient),
		client.WithHost("http://dolphin"),
		client.WithAPIVersionNegotiation(),
	)
}
