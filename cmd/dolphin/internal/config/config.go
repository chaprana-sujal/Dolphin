package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config represents the dolphin client configuration.
type Config struct {
	Host         string // e.g., "prod-server:7777"
	JumpHost     string // e.g., "bastion:7777" (optional)
	IdentityPath string // Defaults to ~/.dolphin
	BindAddr     string // e.g., "localhost:0"
}

// ParseHost ensures the host has a port, defaulting to 7777.
func ParseHost(host string) string {
	if !strings.Contains(host, ":") {
		return host + ":7777"
	}
	return host
}

// DefaultConfigDir returns the default ~/.dolphin path.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".dolphin"
	}
	return filepath.Join(home, ".dolphin")
}

// Validate checks internal consistency.
func (c *Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	return nil
}
