package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultDockerSocket returns the default path or pipe to the docker daemon.
func DefaultDockerSocket() string {
	if runtime.GOOS == "windows" {
		return "//./pipe/docker_engine"
	}
	return "/var/run/docker.sock"
}

// dialDockerEngine connects to the local docker daemon.
func dialDockerEngine(socketPath string) (net.Conn, error) {
	if runtime.GOOS == "windows" {
		// Named pipe support goes here if needed; for now keep it simple or use winio
		// To avoid extra dependencies just fail back to checking tcp or require tcp on windows
		if strings.HasPrefix(socketPath, "//./pipe/") {
			return nil, fmt.Errorf("windows named pipes require winio dependency, please use tcp://")
		}
	}
	
	if strings.HasPrefix(socketPath, "tcp://") {
		return net.Dial("tcp", strings.TrimPrefix(socketPath, "tcp://"))
	}
	return net.Dial("unix", socketPath)
}

// RelayDockerStream bridges a yamux stream to the local docker socket.
func RelayDockerStream(ctx context.Context, stream net.Conn, socketPath string) {
	defer stream.Close()

	dockerConn, err := dialDockerEngine(socketPath)
	if err != nil {
		log.Printf("dolphin/agent: dial docker socket %s failed: %v", socketPath, err)
		return
	}
	defer dockerConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(dockerConn, stream)
		if cw, ok := dockerConn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(stream, dockerConn)
		if cw, ok := stream.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	
	// Wait for cancellation or streams to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		dockerConn.Close()
		stream.Close()
	case <-done:
	}
}

// GetDockerVersion attempts to get the docker daemon version string via a simple info request.
// If it fails, returns "unknown".
func GetDockerVersion(socketPath string) string {
	conn, err := dialDockerEngine(socketPath)
	if err != nil {
		return "unknown (dial error)"
	}
	defer conn.Close()

	// Simple HTTP GET to the /version endpoint
	req := "GET /version HTTP/1.0\r\nHost: "
	if runtime.GOOS == "windows" {
		req += "docker_engine"
	} else {
		req += "docker.sock"
	}
	req += "\r\n\r\n"

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		return "unknown (write error)"
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return "unknown (read error)"
	}

	resp := string(buf[:n])
	// A real implementation would parse the JSON, but simple string search works for quick info
	if strings.Contains(resp, `"Version"`) {
		// Just a heuristic to grab the version part or we can just report ok
		return "ok"
	}
	return "unknown"
}
