package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
)

// Proxy listens locally and forwards connections to yamux streams.
type Proxy struct {
	session   *mux.Session
	listener  net.Listener
	localAddr string
}

// New creates a new Proxy binding to addr. If addr is "localhost:0",
// a random ephemeral port is allocated.
func New(session *mux.Session, bindAddr string) (*Proxy, error) {
	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("dolphin/proxy: listen %s: %w", bindAddr, err)
	}
	return &Proxy{
		session:   session,
		listener:  ln,
		localAddr: ln.Addr().String(),
	}, nil
}

// DockerHost returns the DOCKER_HOST string to connect to this proxy.
func (p *Proxy) DockerHost() string {
	return "tcp://" + p.localAddr
}

// LocalAddr returns the bound host:port.
func (p *Proxy) LocalAddr() string {
	return p.localAddr
}

// Start begins the accept loop. It returns when context is canceled
// or the listener is closed.
func (p *Proxy) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		p.listener.Close()
	}()

	for {
		conn, err := p.listener.Accept()
		if err != nil {
			if mux.ErrIsClosed(err) || ctx.Err() != nil {
				return nil
			}
			// Don't die on temporary accept errors.
			log.Printf("dolphin/proxy: accept error: %v", err)
			continue
		}
		go p.handleConn(conn)
	}
}

// handleConn opens a yamux docker stream and copies birectionally.
func (p *Proxy) handleConn(localConn net.Conn) {
	defer localConn.Close()

	stream, err := p.session.Open(mux.StreamTypeDocker)
	if err != nil {
		log.Printf("dolphin/proxy: open stream: %v", err)
		return
	}
	defer stream.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(stream, localConn)
		// Close the write side of the yamux stream if possible
		if cw, ok := stream.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(localConn, stream)
		if cw, ok := localConn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	wg.Wait()
}
