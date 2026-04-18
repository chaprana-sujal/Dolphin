package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// Request is sent by the client.
type Request struct {
	Op string `json:"op"` // e.g., "ping", "stats"
}

// Response is sent by the agent.
type Response struct {
	Ok      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PingPayload is the payload for a "ping" response.
type PingPayload struct {
	AgentVersion string `json:"agent_version"`
	DockerInfo   string `json:"docker_info,omitempty"`
}

// StatsPayload is the payload for a "stats" response.
type StatsPayload struct {
	NumStreams int `json:"num_streams"`
}

// Client wraps a control stream to the agent.
type Client struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
}

// NewClient creates a control client from a dedicated mux.Stream.
func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:    conn,
		encoder: json.NewEncoder(conn),
		decoder: json.NewDecoder(conn),
	}
}

// call sends a Request and decodes a Response.
func (c *Client) call(req Request, v any) error {
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	if err := c.encoder.Encode(req); err != nil {
		return fmt.Errorf("dolphin/control: encode req: %w", err)
	}

	var resp Response
	if err := c.decoder.Decode(&resp); err != nil {
		return fmt.Errorf("dolphin/control: decode resp: %w", err)
	}

	if !resp.Ok {
		return fmt.Errorf("agent error: %s", resp.Error)
	}

	if v != nil {
		if err := json.Unmarshal(resp.Payload, v); err != nil {
			return fmt.Errorf("dolphin/control: unmarshal payload: %w", err)
		}
	}
	return nil
}

// Ping checks if the agent is alive and returns its version.
func (c *Client) Ping(ctx context.Context) (PingPayload, error) {
	var payload PingPayload
	err := c.call(Request{Op: "ping"}, &payload)
	return payload, err
}

// Stats requests current connection stats from the agent.
func (c *Client) Stats(ctx context.Context) (StatsPayload, error) {
	var payload StatsPayload
	err := c.call(Request{Op: "stats"}, &payload)
	return payload, err
}

// Server handles incoming control requests.
type Server struct {
	conn       net.Conn
	version    string
	streamsFn  func() int
	dockerInfo func() string
}

// NewServer creates a control server to handle a single stream.
func NewServer(conn net.Conn, version string, numStreams func() int, dockerInfo func() string) *Server {
	return &Server{
		conn:       conn,
		version:    version,
		streamsFn:  numStreams,
		dockerInfo: dockerInfo,
	}
}

// Serve handles incoming requests on the control stream until it is closed.
func (s *Server) Serve() error {
	limitedReader := io.LimitReader(s.conn, 4096)
	decoder := json.NewDecoder(limitedReader)
	encoder := json.NewEncoder(s.conn)

	defer s.conn.Close()

	for {
		var req Request
		if err := decoder.Decode(&req); err != nil {
			if errorsIsEOF(err) {
				return nil
			}
			return fmt.Errorf("dolphin/control: decode req: %w", err)
		}

		resp := Response{Ok: true}

		switch req.Op {
		case "ping":
			d := s.dockerInfo()
			p := PingPayload{
				AgentVersion: s.version,
				DockerInfo:   d,
			}
			b, _ := json.Marshal(p)
			resp.Payload = b
		case "stats":
			p := StatsPayload{
				NumStreams: s.streamsFn(),
			}
			b, _ := json.Marshal(p)
			resp.Payload = b
		default:
			resp.Ok = false
			resp.Error = "unknown op"
		}

		s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := encoder.Encode(resp); err != nil {
			return fmt.Errorf("dolphin/control: encode resp: %w", err)
		}
		s.conn.SetWriteDeadline(time.Time{})
	}
}

func errorsIsEOF(err error) bool {
	return err == io.EOF || err.Error() == "EOF"
}
