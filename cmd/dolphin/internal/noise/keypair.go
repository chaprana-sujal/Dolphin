// Package noise provides Curve25519 keypair generation and persistence
// for use with the Noise Protocol handshake.
package noise

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gn "github.com/flynn/noise"
)

// CipherSuite is the fixed cipher suite used by dolphin:
//
//	DH: Curve25519
//	Cipher: ChaCha20-Poly1305
//	Hash: SHA-256
var CipherSuite = gn.NewCipherSuite(gn.DH25519, gn.CipherChaChaPoly, gn.HashSHA256)

// KeyPair wraps a Noise DHKey with helper methods.
type KeyPair struct {
	gn.DHKey
}

// PublicHex returns the public key as a lowercase hex string (64 chars).
func (kp KeyPair) PublicHex() string {
	return hex.EncodeToString(kp.Public)
}

// PrivateHex returns the private key as a lowercase hex string (64 chars).
func (kp KeyPair) PrivateHex() string {
	return hex.EncodeToString(kp.Private)
}

// Fingerprint returns a short 16-char hex prefix of the public key —
// enough to verify identity visually, similar to SSH.
func (kp KeyPair) Fingerprint() string {
	h := kp.PublicHex()
	if len(h) > 16 {
		return h[:16]
	}
	return h
}

// Generate creates a new random Curve25519 keypair.
func Generate() (KeyPair, error) {
	kp, err := CipherSuite.GenerateKeypair(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("dolphin/noise: generate keypair: %w", err)
	}
	return KeyPair{kp}, nil
}

// ParsePublicKey decodes a hex-encoded 32-byte Curve25519 public key.
func ParsePublicKey(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("dolphin/noise: parse public key: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("dolphin/noise: public key must be 32 bytes, got %d", len(b))
	}
	return b, nil
}

// LoadOrCreate loads a KeyPair from dir/identity, creating one if absent.
// The directory is created with mode 0700 if it doesn't exist.
// The identity file is written with mode 0600.
func LoadOrCreate(dir string) (KeyPair, error) {
	path := filepath.Join(dir, "identity")

	data, err := os.ReadFile(path)
	if err == nil {
		return parseStoredKey(strings.TrimSpace(string(data)))
	}
	if !os.IsNotExist(err) {
		return KeyPair{}, fmt.Errorf("dolphin/noise: read identity: %w", err)
	}

	// File not found — generate a new keypair.
	kp, err := Generate()
	if err != nil {
		return KeyPair{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeyPair{}, fmt.Errorf("dolphin/noise: mkdir %s: %w", dir, err)
	}
	// Format: "private_hex:public_hex"
	content := kp.PrivateHex() + ":" + kp.PublicHex()
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		return KeyPair{}, fmt.Errorf("dolphin/noise: write identity: %w", err)
	}
	return kp, nil
}

// parseStoredKey parses "private_hex:public_hex" from disk.
func parseStoredKey(s string) (KeyPair, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return KeyPair{}, fmt.Errorf("dolphin/noise: invalid identity format (expected priv:pub)")
	}
	priv, err := hex.DecodeString(parts[0])
	if err != nil {
		return KeyPair{}, fmt.Errorf("dolphin/noise: decode private key: %w", err)
	}
	pub, err := hex.DecodeString(parts[1])
	if err != nil {
		return KeyPair{}, fmt.Errorf("dolphin/noise: decode public key: %w", err)
	}
	if len(priv) != 32 || len(pub) != 32 {
		return KeyPair{}, fmt.Errorf("dolphin/noise: expected 32-byte keys, got priv=%d pub=%d", len(priv), len(pub))
	}
	return KeyPair{gn.DHKey{Private: priv, Public: pub}}, nil
}
