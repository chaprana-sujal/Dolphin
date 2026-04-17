package mux

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

// StreamType identifies the purpose of a yamux stream.
// It is written as a single byte immediately after opening the stream,
// before any protocol-specific data is exchanged.
type StreamType byte

const (
	// StreamTypeDocker indicates raw Docker API traffic.
	StreamTypeDocker StreamType = 0x01
	// StreamTypeControl indicates the control protocol (ping, stats, shutdown).
	StreamTypeControl StreamType = 0x02
	// StreamTypeRelay indicates a relay stream through a jump host.
	StreamTypeRelay StreamType = 0x03
)

// Session wraps a yamux.Session with our typed stream helpers.
type Session struct {
	inner *yamux.Session
}

// defaultConfig returns an optimized yamux config for dolphin.
func defaultConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	// Detect dead connections faster than standard TCP keepalives.
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 10 * time.Second
	// Use larger windows for better throughput on large transfers (image pulls).
	cfg.MaxStreamWindowSize = 256 * 1024
	cfg.LogOutput = io.Discard // don't spam stderr with yamux logs
	return cfg
}

// NewClient wraps a net.Conn (typically a noise.Conn) as the yamux client.
func NewClient(conn net.Conn) (*Session, error) {
	ys, err := yamux.Client(conn, defaultConfig())
	if err != nil {
		return nil, fmt.Errorf("dolphin/mux: client session: %w", err)
	}
	return &Session{inner: ys}, nil
}

// NewServer wraps a net.Conn as the yamux server.
func NewServer(conn net.Conn) (*Session, error) {
	ys, err := yamux.Server(conn, defaultConfig())
	if err != nil {
		return nil, fmt.Errorf("dolphin/mux: server session: %w", err)
	}
	return &Session{inner: ys}, nil
}

// Open initiates a new stream and writes the given stream type byte.
func (s *Session) Open(t StreamType) (net.Conn, error) {
	stream, err := s.inner.Open()
	if err != nil {
		return nil, fmt.Errorf("dolphin/mux: open stream: %w", err)
	}
	if _, err := stream.Write([]byte{byte(t)}); err != nil {
		stream.Close()
		return nil, fmt.Errorf("dolphin/mux: write stream type: %w", err)
	}
	return stream, nil
}

// Accept blocks until a new stream is opened by the peer, reads the
// stream type byte, and returns the typed stream.
func (s *Session) Accept() (net.Conn, StreamType, error) {
	stream, err := s.inner.Accept()
	if err != nil {
		return nil, 0, fmt.Errorf("dolphin/mux: accept stream: %w", err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(stream, b); err != nil {
		stream.Close()
		return nil, 0, fmt.Errorf("dolphin/mux: read stream type: %w", err)
	}
	return stream, StreamType(b[0]), nil
}

// Close closes the underlying yamux session.
func (s *Session) Close() error {
	return s.inner.Close()
}

// IsClosed returns true if the session is permanently broken.
func (s *Session) IsClosed() bool {
	return s.inner.IsClosed()
}

// NumStreams returns the current number of active streams.
func (s *Session) NumStreams() int {
	return s.inner.NumStreams()
}

// ErrSessionClosed is a helper to check if an error is due to session closure.
func ErrIsClosed(err error) bool {
	return errors.Is(err, yamux.ErrSessionShutdown) || errors.Is(err, io.EOF)
}
