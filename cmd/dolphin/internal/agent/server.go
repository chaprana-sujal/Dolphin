package agent

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"time"

	"github.com/moby/moby/v2/cmd/dolphin/internal/control"
	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
	"github.com/moby/moby/v2/cmd/dolphin/internal/noise"
)

// Config holds agent server parameters.
type Config struct {
	BindAddr     string
	SocketPath   string
	IdentityPath string // Directory containing identity key
	Authorized   [][]byte
	Version      string
}

// Server acts as the dolphin-agent listener.
type Server struct {
	cfg      Config
	localKey noise.KeyPair
	listener net.Listener
}

// NewServer initializes a new agent server.
func NewServer(cfg Config) (*Server, error) {
	kp, err := noise.LoadOrCreate(cfg.IdentityPath)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}

	if cfg.SocketPath == "" {
		cfg.SocketPath = DefaultDockerSocket()
	}

	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", cfg.BindAddr, err)
	}

	log.Printf("dolphin-agent listening on %s", ln.Addr().String())
	log.Printf("Agent Public Key: %s", kp.PublicHex())
	log.Printf("Agent Fingerprint: %s", kp.Fingerprint())
	log.Printf("Docker Socket: %s", cfg.SocketPath)

	return &Server{
		cfg:      cfg,
		localKey: kp,
		listener: ln,
	}, nil
}

// Serve accepts TCP connections and processes them.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	sem := make(chan struct{}, 64)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}
		
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			s.handleConn(ctx, conn)
		}()
	}
}

// handleConn delegates to Noise, establishes yamux, and dispatches streams.
func (s *Server) handleConn(ctx context.Context, tcpConn net.Conn) {
	defer tcpConn.Close()

	// 1. Noise Handshake (IK or XX)
	tcpConn.SetDeadline(time.Now().Add(10 * time.Second))
	noiseConn, remotePub, err := noise.Accept(tcpConn, s.localKey, s.cfg.Authorized)
	if err != nil {
		log.Printf("noise handshake failed from %s: %v", tcpConn.RemoteAddr(), err)
		return
	}
	tcpConn.SetDeadline(time.Time{})
	log.Printf("client connected: %x (from %s)", remotePub[:8], tcpConn.RemoteAddr())

	// 2. Yamux Session Setup
	session, err := mux.NewServer(noiseConn)
	if err != nil {
		log.Printf("yamux server session failed: %v", err)
		return
	}
	defer session.Close()

	// 3. Accept Yamux Streams
	for {
		stream, typ, err := session.Accept()
		if err != nil {
			if !mux.ErrIsClosed(err) {
				log.Printf("yamux accept stream error: %v", err)
			}
			return
		}

		switch typ {
		case mux.StreamTypeDocker:
			go RelayDockerStream(ctx, stream, s.cfg.SocketPath)
		case mux.StreamTypeControl:
			ctrlServer := control.NewServer(
				stream,
				s.cfg.Version,
				func() int { return session.NumStreams() },
				func() string {
					// Use a dummy string or parse actual ver
					return runtime.GOOS + "/" + runtime.GOARCH
				},
			)
			go ctrlServer.Serve()
		default:
			log.Printf("unknown stream type %x, closing stream", byte(typ))
			stream.Close()
		}
	}
}
