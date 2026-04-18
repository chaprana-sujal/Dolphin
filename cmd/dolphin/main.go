package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/moby/moby/v2/cmd/dolphin/internal/client"
	"github.com/moby/moby/v2/cmd/dolphin/internal/config"
	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
	"github.com/moby/moby/v2/cmd/dolphin/internal/noise"
	"github.com/moby/moby/v2/cmd/dolphin/internal/tui"
	"github.com/moby/moby/v2/cmd/dolphin/internal/tui/views"
	"time"
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

	dashboardCmd := &cobra.Command{
		Use:   "dashboard [host]",
		Short: "Start the Terminal UI Dashboard",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Host = args[0]
			return runDashboard(context.Background(), cfg)
		},
	}
	dashboardCmd.Flags().StringVarP(&cfg.IdentityPath, "identity", "i", "", "Identity directory (default ~/.dolphin)")

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
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(keygenCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDashboard(ctx context.Context, cfg config.Config) error {
	identityDir := cfg.IdentityPath
	if identityDir == "" {
		identityDir = config.DefaultConfigDir()
	}

	localKey, err := noise.LoadOrCreate(identityDir)
	if err != nil {
		return err
	}
	knownHosts, err := noise.LoadKnownHosts(identityDir)
	if err != nil {
		return err
	}

	target := config.ParseHost(cfg.Host)
	expectedPub := knownHosts.Lookup(target)
	
	// Fast path Noise connect (No TOFU prompt in UI)
	noiseConn, _, err := noise.Dial(ctx, target, localKey, expectedPub)
	if err != nil {
		return fmt.Errorf("dashboard connect error (is host pinned in known_hosts?): %w", err)
	}

	session, err := mux.NewClient(noiseConn)
	if err != nil {
		noiseConn.Close()
		return err
	}
	defer session.Close()

	app, err := tui.NewApp(session)
	if err != nil {
		return err
	}
	app.InitViews()

	dashboard := views.NewDashboard(app)
	details := views.NewDetails(app)

	dashboard.OnSelect = func(containerID string, name string) {
		details.Load(containerID, name)
		app.Pages.SwitchToPage("details")
	}

	app.Pages.AddPage("dashboard", dashboard.Layout, true, true)
	app.Pages.AddPage("details", details.Layout, true, false)

	// Background refresher
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-app.Ctx.Done():
				return
			case <-ticker.C:
				dashboard.Refresh()
			}
		}
	}()

	dashboard.Refresh()
	return app.Run()
}
