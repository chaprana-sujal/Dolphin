package noise

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrUnknownHost is returned by Verify when the host has no pinned key.
var ErrUnknownHost = errors.New("dolphin/noise: unknown host — key not pinned")

// ErrKeyMismatch is returned when the remote key doesn't match the pinned key.
var ErrKeyMismatch = errors.New("dolphin/noise: host key mismatch — possible MITM")

// KnownHostEntry stores a pinned static public key for a single host.
type KnownHostEntry struct {
	Hostname    string    `json:"hostname"`
	PublicKey   string    `json:"public_key"`   // 64-char lowercase hex
	Fingerprint string    `json:"fingerprint"`   // 16-char hex prefix (for display)
	AddedAt     time.Time `json:"added_at"`
}

// KnownHosts is the in-memory registry backed by ~/.dolphin/known_hosts.json.
type KnownHosts struct {
	path  string
	Hosts map[string]KnownHostEntry `json:"hosts"`
}

// LoadKnownHosts loads (or initialises) the known_hosts.json file at dir/known_hosts.json.
func LoadKnownHosts(dir string) (*KnownHosts, error) {
	path := filepath.Join(dir, "known_hosts.json")
	kh := &KnownHosts{
		path:  path,
		Hosts: make(map[string]KnownHostEntry),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return kh, nil // fresh start, no file yet
		}
		return nil, fmt.Errorf("dolphin/noise: load known_hosts: %w", err)
	}
	if err := json.Unmarshal(data, kh); err != nil {
		return nil, fmt.Errorf("dolphin/noise: parse known_hosts: %w", err)
	}
	kh.path = path
	return kh, nil
}

// Lookup returns the pinned public key bytes for hostname, or nil if not found.
func (kh *KnownHosts) Lookup(hostname string) []byte {
	entry, ok := kh.Hosts[hostname]
	if !ok {
		return nil
	}
	b, err := ParsePublicKey(entry.PublicKey)
	if err != nil {
		return nil
	}
	return b
}

// Verify checks that pubkey matches the pinned key for hostname.
//
//   - Returns nil on success.
//   - Returns ErrUnknownHost if hostname has no pinned key.
//   - Returns ErrKeyMismatch (hard reject) if the key doesn't match.
func (kh *KnownHosts) Verify(hostname string, pubkey []byte) error {
	pinned := kh.Lookup(hostname)
	if pinned == nil {
		return ErrUnknownHost
	}
	if len(pinned) != len(pubkey) {
		return ErrKeyMismatch
	}
	for i := range pinned {
		if pinned[i] != pubkey[i] {
			return fmt.Errorf("%w for %s", ErrKeyMismatch, hostname)
		}
	}
	return nil
}

// Add pins pubkey for hostname and persists to disk immediately.
// Overwrites any existing entry for hostname.
func (kh *KnownHosts) Add(hostname string, pubkey []byte) error {
	kh.Hosts[hostname] = KnownHostEntry{
		Hostname:    hostname,
		PublicKey:   fmt.Sprintf("%x", pubkey),
		Fingerprint: fmt.Sprintf("%x", pubkey[:8]),
		AddedAt:     time.Now().UTC(),
	}
	return kh.save()
}

// Remove deletes the pinned key for hostname and persists.
func (kh *KnownHosts) Remove(hostname string) error {
	delete(kh.Hosts, hostname)
	return kh.save()
}

func (kh *KnownHosts) save() error {
	if err := os.MkdirAll(filepath.Dir(kh.path), 0o700); err != nil {
		return fmt.Errorf("dolphin/noise: mkdir for known_hosts: %w", err)
	}
	data, err := json.MarshalIndent(kh, "", "  ")
	if err != nil {
		return fmt.Errorf("dolphin/noise: marshal known_hosts: %w", err)
	}
	return os.WriteFile(kh.path, data, 0o600)
}
