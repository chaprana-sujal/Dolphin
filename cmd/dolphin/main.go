package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/moby/moby/v2/cmd/dolphin/internal/client"
	"github.com/moby/moby/v2/cmd/dolphin/internal/config"
	"github.com/moby/moby/v2/cmd/dolphin/internal/noise"
)

func main() {
	var cfg config.Config

	rootCmd := &cobra.Command{
		Use:   "dolphin",
		Short: "Noise-encrypted multiplexed Docker bridge",
	}

	connectCmd := &cobra.Command{
		Use:   "connect [host]",
		Short: "Connect to a remote dolphin-agent and expose a local docker proxy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Host = args[0]
			return client.RunWithReconnectPrompt(context.Background(), cfg)
		},
	}

	connectCmd.Flags().StringVarP(&cfg.BindAddr, "bind", "b", "localhost:0", "Local address to bind proxy")
	connectCmd.Flags().StringVarP(&cfg.IdentityPath, "identity", "i", "", "Identity directory (default ~/.dolphin)")

	keygenCmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate and display a new local identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := cfg.IdentityPath
			if dir == "" {
				dir = config.DefaultConfigDir()
			}
			kp, err := noise.LoadOrCreate(dir)
			if err != nil {
				return err
			}
			fmt.Printf("Identity key loaded/created at %s\n", dir)
			fmt.Printf("Public Key: %s\n", kp.PublicHex())
			fmt.Printf("Fingerprint: %s\n", kp.Fingerprint())
			return nil
		},
	}
	keygenCmd.Flags().StringVarP(&cfg.IdentityPath, "identity", "i", "", "Identity directory (default ~/.dolphin)")

	rootCmd.AddCommand(connectCmd)
	rootCmd.AddCommand(keygenCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
