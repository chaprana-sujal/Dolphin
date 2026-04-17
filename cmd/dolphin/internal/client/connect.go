package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moby/moby/v2/cmd/dolphin/internal/config"
	"github.com/moby/moby/v2/cmd/dolphin/internal/control"
	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
	"github.com/moby/moby/v2/cmd/dolphin/internal/noise"
	"github.com/moby/moby/v2/cmd/dolphin/internal/proxy"
)

// Connect ties everything together: Noise Dial -> Yamux -> Proxy.
func Connect(ctx context.Context, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	identityDir := cfg.IdentityPath
	if identityDir == "" {
		identityDir = config.DefaultConfigDir()
	}

	// 1. Load keys and known_hosts.
	localKey, err := noise.LoadOrCreate(identityDir)
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	
	knownHosts, err := noise.LoadKnownHosts(identityDir)
	if err != nil {
		return err
	}

	// 2. Determine target host and check known_hosts
	target := config.ParseHost(cfg.Host)
	expectedPub := knownHosts.Lookup(target)
	
	// Fast path Noise connect
	log.Printf("Dialing %s...", target)
	ctxDial, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()

	noiseConn, remotePub, err := noise.Dial(ctxDial, target, localKey, expectedPub)
	if err != nil {
		return err
	}

	// 3. TOFU verify
	if expectedPub == nil {
		fmt.Printf("\nThe authenticity of host '%s' can't be established.\n", target)
		fmt.Printf("Dolphin key fingerprint: %s (Curve25519).\n", noise.KeyPair{DHKey: localKey.DHKey}.Fingerprint())
		fmt.Print("Are you sure you want to continue connecting? (yes/no) ")
		
		var ans string
		fmt.Scanln(&ans)
		if ans != "yes" && ans != "y" {
			noiseConn.Close()
			return fmt.Errorf("Host verification aborted")
		}
		
		if err := knownHosts.Add(target, remotePub); err != nil {
			log.Printf("Warning: failed to save known_hosts: %v", err)
		} else {
			log.Printf("Warning: Permanently added '%s' to the list of known hosts.", target)
		}
	} else if err := knownHosts.Verify(target, remotePub); err != nil {
		noiseConn.Close()
		return err
	}

	// 4. Wrap with Yamux
	session, err := mux.NewClient(noiseConn)
	if err != nil {
		noiseConn.Close()
		return fmt.Errorf("start yamux session: %w", err)
	}
	defer session.Close()

	// 5. Test control stream (Ping)
	ctrlStream, err := session.Open(mux.StreamTypeControl)
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	
	ctrlClient := control.NewClient(ctrlStream)
	ctxPing, cancelPing := context.WithTimeout(ctx, 3*time.Second)
	defer cancelPing()
	
	pong, err := ctrlClient.Ping(ctxPing)
	if err != nil {
		log.Printf("Warning: control ping failed (agent may be older version): %v", err)
	} else {
		log.Printf("Connected! Agent: %s, Docker: %s", pong.AgentVersion, pong.DockerInfo)
	}
	
	// We keep the control stream open or close it and re-open on demand.
	// For simplicity, we can close the initial test stream.
	ctrlStream.Close()

	// 6. Start Docker Proxy
	p, err := proxy.New(session, cfg.BindAddr)
	if err != nil {
		return err
	}
	
	fmt.Println()
	fmt.Printf("✨ Bridge active -> DOCKER_HOST=%s\n", p.DockerHost())
	fmt.Printf("Waiting for connections. Press Ctrl+C to disconnect.\n")

	// 7. Wait for disconnect or signal
	proxyCtx, proxyCancel := context.WithCancel(ctx)
	defer proxyCancel()

	go func() {
		// Wait for session to die
		<-session.CloseChan()
		log.Printf("Session closed.")
		proxyCancel()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		select {
		case <-sigCh:
			log.Printf("Shutting down...")
			proxyCancel()
		case <-proxyCtx.Done():
		}
	}()

	return p.Start(proxyCtx)
}

// Interactive prompt wrapping Connect.
func RunWithReconnectPrompt(ctx context.Context, cfg config.Config) error {
	for {
		err := Connect(ctx, cfg)
		// Clean exit or user cancel
		if err == nil || ctx.Err() != nil || err.Error() == "Host verification aborted" {
			return err
		}
		
		fmt.Printf("\n⚠ Connection was lost: %v\n", err)
		fmt.Printf("Reconnect to %s? [y/N] ", cfg.Host)
		var ans string
		fmt.Scanln(&ans)
		if ans != "yes" && ans != "y" {
			return err
		}
		fmt.Println("Reconnecting...")
	}
}
