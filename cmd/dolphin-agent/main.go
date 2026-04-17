package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/moby/moby/v2/cmd/dolphin/internal/agent"
	"github.com/moby/moby/v2/cmd/dolphin/internal/config"
	"github.com/moby/moby/v2/cmd/dolphin/internal/noise"
)

var version = "0.1.0"

func main() {
	var cfg agent.Config
	var authorizedKeys []string

	rootCmd := &cobra.Command{
		Use:   "dolphin-agent",
		Short: "Server component for the dolphin encrypted docker bridge",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.IdentityPath == "" {
				cfg.IdentityPath = config.DefaultConfigDir()
			}
			
			// Load authorized keys if any given via flag
			for _, k := range authorizedKeys {
				pub, err := noise.ParsePublicKey(k)
				if err != nil {
					return fmt.Errorf("parse authorized key %q: %w", k, err)
				}
				cfg.Authorized = append(cfg.Authorized, pub)
			}
			
			cfg.Version = version

			srv, err := agent.NewServer(cfg)
			if err != nil {
				return err
			}
			
			return srv.Serve(context.Background())
		},
	}

	rootCmd.Flags().StringVarP(&cfg.BindAddr, "bind", "b", "0.0.0.0:7777", "Address to listen on")
	rootCmd.Flags().StringVarP(&cfg.SocketPath, "socket", "s", "", "Path to docker socket (default auto)")
	rootCmd.Flags().StringVarP(&cfg.IdentityPath, "identity", "i", "", "Identity directory (default ~/.dolphin)")
	rootCmd.Flags().StringSliceVarP(&authorizedKeys, "authorized", "a", nil, "Authorized public keys (hex)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
