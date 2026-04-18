// Package noise implements the Noise Protocol handshake for dolphin.
//
// Two patterns are supported:
//
//   - IK  (1 RTT): used when the remote host's static public key is already
//     pinned in known_hosts.json. The initiator encrypts their identity in the
//     very first message — zero round-trip overhead after connect.
//
//   - XX  (2 RTT): used on first-contact (TOFU). Neither side knows the other's
//     static key beforehand. After the handshake completes the server's key is
//     presented to the user for pinning.
//
// The first byte sent by the initiator before any handshake frames is a
// pattern selector (PatternIK or PatternXX) so the responder knows which
// state machine to run.
package noise

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"

	gn "github.com/flynn/noise"
)

// Pattern selection bytes — sent unencrypted as the very first byte.
const (
	PatternIK byte = 0x01 // Fast path: server key is known
	PatternXX byte = 0x02 // TOFU path: server key is unknown
)

// Dial connects to addr and performs a Noise handshake as the initiator.
//
//   - If remotePublicKey is non-nil, uses IK (1 RTT, known-host fast path).
//   - If remotePublicKey is nil, uses XX (2 RTT, TOFU path).
//
// Returns the encrypted Conn (ready for yamux) and the remote's static public key.
func Dial(ctx context.Context, addr string, localKey KeyPair, remotePublicKey []byte) (net.Conn, []byte, error) {
	inner, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: dial %s: %w", addr, err)
	}

	var nc *Conn
	var remotePub []byte

	if remotePublicKey != nil {
		// Fast path — IK.
		if _, err := inner.Write([]byte{PatternIK}); err != nil {
			inner.Close()
			return nil, nil, fmt.Errorf("dolphin/noise: write pattern byte: %w", err)
		}
		nc, err = ikInitiate(inner, localKey, remotePublicKey)
		remotePub = remotePublicKey
	} else {
		// TOFU path — XX.
		if _, err := inner.Write([]byte{PatternXX}); err != nil {
			inner.Close()
			return nil, nil, fmt.Errorf("dolphin/noise: write pattern byte: %w", err)
		}
		nc, remotePub, err = xxInitiate(inner, localKey)
	}
	if err != nil {
		inner.Close()
		return nil, nil, err
	}
	return nc, remotePub, nil
}

// Accept waits for a Noise handshake from an incoming net.Conn.
//
// authorizedKeys is an optional allowlist of client static public keys.
// Pass nil to accept any client (useful before authorized_keys is set up).
//
// Returns the encrypted Conn and the client's static public key.
func Accept(conn net.Conn, localKey KeyPair, authorizedKeys [][]byte) (net.Conn, []byte, error) {
	pat := make([]byte, 1)
	if _, err := conn.Read(pat); err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: read pattern byte: %w", err)
	}

	var nc *Conn
	var remotePub []byte
	var err error

	switch pat[0] {
	case PatternIK:
		nc, remotePub, err = ikRespond(conn, localKey)
	case PatternXX:
		nc, remotePub, err = xxRespond(conn, localKey)
	default:
		return nil, nil, fmt.Errorf("dolphin/noise: unknown pattern 0x%02x", pat[0])
	}
	if err != nil {
		return nil, nil, err
	}

	// Enforce key authorization if a list was provided.
	if authorizedKeys != nil {
		if !isAuthorized(remotePub, authorizedKeys) {
			nc.Close()
			return nil, nil, errors.New("dolphin/noise: client key not in authorized_keys")
		}
	}
	return nc, remotePub, nil
}

// ── IK (1 RTT) ───────────────────────────────────────────────────────────────
//
// IK message flow:
//   → e, es, s, ss   (initiator → responder, 1 message)
//   ← e, ee, se      (responder → initiator, 1 message)

func ikInitiate(inner net.Conn, local KeyPair, remotePub []byte) (*Conn, error) {
	hs, err := gn.NewHandshakeState(gn.Config{
		CipherSuite:   CipherSuite,
		Random:        rand.Reader,
		Pattern:       gn.HandshakeIK,
		Initiator:     true,
		StaticKeypair: local.DHKey,
		PeerStatic:    remotePub,
	})
	if err != nil {
		return nil, fmt.Errorf("dolphin/noise: IK init state: %w", err)
	}

	// → e, es, s, ss
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("dolphin/noise: IK write msg1: %w", err)
	}
	if err := writeFrame(inner, msg); err != nil {
		return nil, fmt.Errorf("dolphin/noise: IK send msg1: %w", err)
	}

	// ← e, ee, se
	frame, err := readFrame(inner)
	if err != nil {
		return nil, fmt.Errorf("dolphin/noise: IK read msg2: %w", err)
	}
	_, cs1, cs2, err := hs.ReadMessage(nil, frame)
	if err != nil {
		return nil, fmt.Errorf("dolphin/noise: IK process msg2: %w", err)
	}
	return NewConn(inner, cs1, cs2, true), nil
}

func ikRespond(inner net.Conn, local KeyPair) (*Conn, []byte, error) {
	hs, err := gn.NewHandshakeState(gn.Config{
		CipherSuite:   CipherSuite,
		Random:        rand.Reader,
		Pattern:       gn.HandshakeIK,
		Initiator:     false,
		StaticKeypair: local.DHKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: IK resp state: %w", err)
	}

	// ← e, es, s, ss  (read from initiator)
	frame, err := readFrame(inner)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: IK read msg1: %w", err)
	}
	if _, _, _, err = hs.ReadMessage(nil, frame); err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: IK process msg1: %w", err)
	}

	// Capture the initiator's static public key (decrypted from msg1).
	remotePub := make([]byte, 32)
	copy(remotePub, hs.PeerStatic())

	// → e, ee, se
	msg, cs1, cs2, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: IK write msg2: %w", err)
	}
	if err := writeFrame(inner, msg); err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: IK send msg2: %w", err)
	}
	return NewConn(inner, cs1, cs2, false), remotePub, nil
}

// ── XX (2 RTT, TOFU) ──────────────────────────────────────────────────────────
//
// XX message flow:
//   → e                  (msg 1)
//   ← e, ee, s, es       (msg 2)
//   → s, se              (msg 3)

func xxInitiate(inner net.Conn, local KeyPair) (*Conn, []byte, error) {
	hs, err := gn.NewHandshakeState(gn.Config{
		CipherSuite:   CipherSuite,
		Random:        rand.Reader,
		Pattern:       gn.HandshakeXX,
		Initiator:     true,
		StaticKeypair: local.DHKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX init state: %w", err)
	}

	// → e
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX write msg1: %w", err)
	}
	if err := writeFrame(inner, msg); err != nil {
		return nil, nil, err
	}

	// ← e, ee, s, es
	frame, err := readFrame(inner)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX read msg2: %w", err)
	}
	if _, _, _, err = hs.ReadMessage(nil, frame); err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX process msg2: %w", err)
	}
	remotePub := make([]byte, 32)
	copy(remotePub, hs.PeerStatic())

	// → s, se
	msg, cs1, cs2, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX write msg3: %w", err)
	}
	if err := writeFrame(inner, msg); err != nil {
		return nil, nil, err
	}
	return NewConn(inner, cs1, cs2, true), remotePub, nil
}

func xxRespond(inner net.Conn, local KeyPair) (*Conn, []byte, error) {
	hs, err := gn.NewHandshakeState(gn.Config{
		CipherSuite:   CipherSuite,
		Random:        rand.Reader,
		Pattern:       gn.HandshakeXX,
		Initiator:     false,
		StaticKeypair: local.DHKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX resp state: %w", err)
	}

	// ← e
	frame, err := readFrame(inner)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX read msg1: %w", err)
	}
	if _, _, _, err = hs.ReadMessage(nil, frame); err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX process msg1: %w", err)
	}

	// → e, ee, s, es
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX write msg2: %w", err)
	}
	if err := writeFrame(inner, msg); err != nil {
		return nil, nil, err
	}

	// ← s, se
	frame, err = readFrame(inner)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX read msg3: %w", err)
	}
	_, cs1, cs2, err := hs.ReadMessage(nil, frame)
	if err != nil {
		return nil, nil, fmt.Errorf("dolphin/noise: XX process msg3: %w", err)
	}
	remotePub := make([]byte, 32)
	copy(remotePub, hs.PeerStatic())
	return NewConn(inner, cs1, cs2, false), remotePub, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// isAuthorized returns true if pub matches any key in the authorized list.
func isAuthorized(pub []byte, authorized [][]byte) bool {
	for _, a := range authorized {
		if len(a) != len(pub) {
			continue
		}
		if subtle.ConstantTimeCompare(a, pub) == 1 {
			return true
		}
	}
	return false
}
