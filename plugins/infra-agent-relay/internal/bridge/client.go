package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
)

// QueuedAck is the server's acknowledgement payload.
type QueuedAck struct {
	ID       int64 `json:"id"`
	Position int   `json:"pos"`
}

// ResponseEnd signals a completed job.
type ResponseEnd struct {
	ID int64 `json:"id"`
}

// ErrorPayload is the server's error message.
type ErrorPayload struct {
	ID      int64  `json:"id,omitempty"`
	Message string `json:"message"`
}

// Client connects to an agent-bridge instance over TCP.
type Client struct {
	addr   string
	conn   net.Conn
	reader *bufio.Reader

	mu sync.Mutex
}

// NewClient creates a bridge client for the given address (host:port).
func NewClient(addr string) *Client {
	return &Client{addr: addr}
}

// Connect establishes the TCP connection.
func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		return fmt.Errorf("connect to agent-bridge at %s: %w", c.addr, err)
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	log.Printf("bridge-client: connected to %s", c.addr)
	return nil
}

// SendPrompt sends a prompt and returns the queued ack.
func (c *Client) SendPrompt(prompt string) (*QueuedAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := WriteMessage(c.conn, Message{Type: MsgPrompt, Payload: []byte(prompt)}); err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	// Read the QUEUED ack.
	msg, err := ReadMessage(c.reader)
	if err != nil {
		return nil, fmt.Errorf("read ack: %w", err)
	}

	if msg.Type == MsgError {
		var ep ErrorPayload
		json.Unmarshal(msg.Payload, &ep)
		return nil, fmt.Errorf("bridge error: %s", ep.Message)
	}

	if msg.Type != MsgQueued {
		return nil, fmt.Errorf("unexpected message type: 0x%02x", msg.Type)
	}

	var ack QueuedAck
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		return nil, fmt.Errorf("decode ack: %w", err)
	}

	return &ack, nil
}

// SendCommand sends a slash command and returns the queued ack.
func (c *Client) SendCommand(cmd string) (*QueuedAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := WriteMessage(c.conn, Message{Type: MsgCommand, Payload: []byte(cmd)}); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	msg, err := ReadMessage(c.reader)
	if err != nil {
		return nil, fmt.Errorf("read ack: %w", err)
	}

	if msg.Type == MsgError {
		var ep ErrorPayload
		json.Unmarshal(msg.Payload, &ep)
		return nil, fmt.Errorf("bridge error: %s", ep.Message)
	}

	if msg.Type != MsgQueued {
		return nil, fmt.Errorf("unexpected message type: 0x%02x", msg.Type)
	}

	var ack QueuedAck
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		return nil, fmt.Errorf("decode ack: %w", err)
	}

	return &ack, nil
}

// ReadResponse reads messages until a ResponseEnd is received.
// Returns the accumulated response text.
func (c *Client) ReadResponse() (string, error) {
	var response string

	for {
		msg, err := ReadMessage(c.reader)
		if err != nil {
			return response, fmt.Errorf("read response: %w", err)
		}

		switch msg.Type {
		case MsgResponseLine:
			response += string(msg.Payload)

		case MsgResponseEnd:
			return response, nil

		case MsgError:
			var ep ErrorPayload
			json.Unmarshal(msg.Payload, &ep)
			return response, fmt.Errorf("agent error: %s", ep.Message)

		default:
			log.Printf("bridge-client: unexpected message type during response: 0x%02x", msg.Type)
		}
	}
}

// Close closes the connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
