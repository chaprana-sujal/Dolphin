package noise

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	gn "github.com/flynn/noise"
)

// maxPlaintext is the maximum plaintext bytes per Noise frame.
// Noise max message = 65535 bytes; AEAD tag = 16 bytes → max plaintext = 65519.
const maxPlaintext = 65519

// Conn wraps a net.Conn with Noise AEAD encryption.
//
// Wire format for every write:
//
//	[uint16 big-endian: ciphertext length][ciphertext bytes]
//
// Each call to Write chunks the plaintext into ≤65519 byte pieces,
// encrypts each piece, and sends a length-prefixed frame.
// Read buffers one decrypted frame at a time to satisfy arbitrary Read sizes.
type Conn struct {
	inner  net.Conn
	sendCS *gn.CipherState
	recvCS *gn.CipherState

	// readMu protects readBuf — allows callers with small buffers to consume
	// a single decrypted Noise frame across multiple Read calls.
	readMu  sync.Mutex
	readBuf []byte

	// writeMu serialises writes so concurrent goroutines don't interleave frames.
	writeMu sync.Mutex
}

// NewConn constructs a noise-encrypted Conn from the two post-handshake
// CipherStates returned by the Noise library.
//
// Noise convention:
//   - cs1 encrypts Initiator → Responder messages
//   - cs2 encrypts Responder → Initiator messages
//
// So the initiator sets sendCS=cs1, recvCS=cs2, and the responder inverts.
func NewConn(inner net.Conn, cs1, cs2 *gn.CipherState, initiator bool) *Conn {
	if initiator {
		return &Conn{inner: inner, sendCS: cs1, recvCS: cs2}
	}
	return &Conn{inner: inner, sendCS: cs2, recvCS: cs1}
}

// Write encrypts p and sends it as one or more length-prefixed Noise frames.
func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxPlaintext {
			chunk = p[:maxPlaintext]
		}
		ciphertext, err := c.sendCS.Encrypt(nil, nil, chunk)
		if err != nil {
			return total, fmt.Errorf("dolphin/noise: encrypt: %w", err)
		}
		if err := writeFrame(c.inner, ciphertext); err != nil {
			return total, fmt.Errorf("dolphin/noise: write frame: %w", err)
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// Read decrypts data from the underlying connection into b.
// If the decrypted frame is larger than b, the remainder is buffered for
// the next Read call — matching standard io.Reader semantics.
func (c *Conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	// Drain existing buffer before reading a new frame.
	if len(c.readBuf) == 0 {
		frame, err := readFrame(c.inner)
		if err != nil {
			return 0, fmt.Errorf("dolphin/noise: read frame: %w", err)
		}
		plain, err := c.recvCS.Decrypt(nil, nil, frame)
		if err != nil {
			return 0, fmt.Errorf("dolphin/noise: decrypt: %w", err)
		}
		c.readBuf = plain
	}

	n := copy(b, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.inner.Close() }

// LocalAddr returns the local network address.
func (c *Conn) LocalAddr() net.Addr { return c.inner.LocalAddr() }

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr { return c.inner.RemoteAddr() }

// SetDeadline sets the read and write deadlines.
func (c *Conn) SetDeadline(t time.Time) error { return c.inner.SetDeadline(t) }

// SetReadDeadline sets the deadline for Read calls.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.inner.SetReadDeadline(t) }

// SetWriteDeadline sets the deadline for Write calls.
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }

// writeFrame sends [uint16 length][data] over w.
func writeFrame(w io.Writer, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("dolphin/noise: frame exceeds max size (%d bytes)", len(data))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// readFrame reads a [uint16 length][data] frame from r.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint16(hdr[:])
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
