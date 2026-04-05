package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

// appServer manages a persistent `codex app-server` subprocess over websocket.
type appServer struct {
	proc   *exec.Cmd
	conn   *websocket.Conn
	mu     sync.Mutex // serializes reads
	writeMu sync.Mutex // serializes writes
	nextID atomic.Int64
	alive  bool
	debug  bool
	port   int
}

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCMessage is a raw incoming JSON-RPC message.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// notification is a server-sent JSON-RPC notification.
type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// findFreePort finds an available TCP port on localhost.
func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// start spawns the app-server process listening on a websocket port.
func (s *appServer) start(binary string, env []string) error {
	port, err := findFreePort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	s.port = port

	listenURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	cmd := exec.Command(binary, "app-server", "--listen", listenURL)
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start app-server: %w", err)
	}
	s.proc = cmd
	s.alive = true

	if s.debug {
		log.Printf("[codex-cli] app-server started (pid %d, ws port %d)", cmd.Process.Pid, port)
	}

	go func() {
		err := cmd.Wait()
		s.alive = false
		if s.debug {
			log.Printf("[codex-cli] app-server exited: %v", err)
		}
	}()

	// Connect websocket — no compression to avoid RSV bit issues.
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	var conn *websocket.Conn
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, _, err = websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			CompressionMode: websocket.CompressionDisabled,
		})
		cancel()
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		s.stop()
		return fmt.Errorf("connect ws: %w", err)
	}
	// Remove default read limit (default is 32768 bytes).
	conn.SetReadLimit(-1)
	s.conn = conn

	if s.debug {
		log.Printf("[codex-cli] websocket connected to %s (no compression)", wsURL)
	}

	return nil
}

// stop kills the app-server process and closes the websocket.
func (s *appServer) stop() {
	if s.conn != nil {
		s.conn.Close(websocket.StatusNormalClosure, "shutdown")
		s.conn = nil
	}
	if s.proc != nil && s.proc.Process != nil {
		s.proc.Process.Kill()
	}
	s.alive = false
	if s.debug {
		log.Printf("[codex-cli] app-server stopped")
	}
}

// sendRequest sends a JSON-RPC request and reads the response.
// Notifications received while waiting are passed to notifyCb (if non-nil).
func (s *appServer) sendRequest(method string, params interface{}, notifyCb func(notification)) (json.RawMessage, error) {
	id := s.nextID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", method, err)
	}

	s.writeMu.Lock()
	err = s.conn.Write(context.Background(), websocket.MessageText, data)
	s.writeMu.Unlock()
	if err != nil {
		s.alive = false
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	if s.debug {
		log.Printf("[codex-cli] → %s (id=%d)", method, id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		_, msgData, err := s.conn.Read(context.Background())
		if err != nil {
			s.alive = false
			return nil, fmt.Errorf("read: %w", err)
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		// Notification.
		if msg.ID == nil && msg.Method != "" {
			if s.debug {
				log.Printf("[codex-cli] ← notify: %s", msg.Method)
			}
			if notifyCb != nil {
				notifyCb(notification{Method: msg.Method, Params: msg.Params})
			}
			continue
		}

		// Response matching our request.
		if msg.ID != nil && *msg.ID == id {
			if msg.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			if s.debug {
				log.Printf("[codex-cli] ← response id=%d (%d bytes)", id, len(msg.Result))
			}
			return msg.Result, nil
		}
	}
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (s *appServer) sendNotification(method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(context.Background(), websocket.MessageText, data)
}

// readNotifications reads websocket messages until a terminal notification.
func (s *appServer) readNotifications(onNotify func(notification)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		_, msgData, err := s.conn.Read(context.Background())
		if err != nil {
			s.alive = false
			return fmt.Errorf("read: %w", err)
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		if msg.ID != nil || msg.Method == "" {
			continue
		}

		n := notification{Method: msg.Method, Params: msg.Params}
		onNotify(n)

		if msg.Method == "turn/completed" || msg.Method == "codex/event/task_complete" ||
			msg.Method == "notifications/turnCompleted" || msg.Method == "notifications/error" {
			return nil
		}
	}
}

// initialize sends the initialize handshake (matching the TypeScript client).
func (s *appServer) initialize() error {
	_, err := s.sendRequest("initialize", map[string]interface{}{
		"protocolVersion": "1.0",
		"clientInfo": map[string]string{
			"name":    "teamagentica-agent-openai",
			"version": "1.0.0",
		},
		"capabilities": map[string]bool{
			"experimentalApi": true,
		},
	}, nil)
	if err != nil {
		return err
	}
	// Send initialized notification (required by protocol).
	return s.sendNotification("initialized", map[string]interface{}{})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
