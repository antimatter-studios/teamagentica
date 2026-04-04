package codexcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
)

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`  // nil for notifications
	Method  string          `json:"method,omitempty"` // set for notifications
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"` // notification params
}

// jsonRPCError is a JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// notification is a server-sent JSON-RPC notification.
type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// appServer manages a persistent `codex app-server` subprocess.
type appServer struct {
	mu       sync.Mutex
	proc     *exec.Cmd
	stdin    io.WriteCloser
	scanner  *bufio.Scanner
	nextID   atomic.Int64
	alive    bool
	debug    bool

	// notifications is a buffered channel for notifications received
	// while waiting for a response to a request.
	notifications chan notification
}

// newAppServer creates an unstarted app server manager.
func newAppServer(debug bool) *appServer {
	return &appServer{
		debug:         debug,
		notifications: make(chan notification, 256),
	}
}

// start spawns the app-server process.
func (s *appServer) start(binary string, env []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.alive {
		return nil
	}

	cmd := exec.Command(binary, "app-server")
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start app-server: %w", err)
	}

	s.proc = cmd
	s.stdin = stdin
	s.scanner = bufio.NewScanner(stdout)
	s.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB line buffer
	s.alive = true

	if s.debug {
		log.Printf("[codex-cli] app-server started (pid %d)", cmd.Process.Pid)
	}

	// Monitor process exit in background.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.alive = false
		s.mu.Unlock()
		if s.debug {
			log.Printf("[codex-cli] app-server exited: %v", err)
		}
	}()

	return nil
}

// stop kills the app-server process.
func (s *appServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.alive || s.proc == nil {
		return
	}
	s.stdin.Close()
	s.proc.Process.Kill()
	s.alive = false
	if s.debug {
		log.Printf("[codex-cli] app-server stopped")
	}
}

// isAlive checks if the process is still running.
func (s *appServer) isAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// sendRequest sends a JSON-RPC request and returns the result.
// Notifications received while waiting for the response are buffered.
// Caller must hold no locks (this reads from stdout which blocks).
func (s *appServer) sendRequest(method string, params interface{}) (json.RawMessage, error) {
	id := s.nextID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if s.debug {
		log.Printf("[codex-cli] → %s", string(data[:len(data)-1]))
	}

	if _, err := s.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read lines until we get a response with matching ID.
	for {
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}
			return nil, fmt.Errorf("app-server stdout closed")
		}

		line := s.scanner.Text()
		if line == "" {
			continue
		}

		if s.debug {
			log.Printf("[codex-cli] ← %s", truncateLog(line, 200))
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			if s.debug {
				log.Printf("[codex-cli] skip unparseable line: %s", truncateLog(line, 100))
			}
			continue
		}

		// Check if this is a notification (no id, has method).
		if resp.ID == nil && resp.Method != "" {
			select {
			case s.notifications <- notification{Method: resp.Method, Params: resp.Params}:
			default:
				if s.debug {
					log.Printf("[codex-cli] notification buffer full, dropping: %s", resp.Method)
				}
			}
			continue
		}

		// Check if this response matches our request.
		if resp.ID != nil && *resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}

		// Unexpected response ID — skip it.
		if s.debug {
			log.Printf("[codex-cli] unexpected response id: %v (waiting for %d)", resp.ID, id)
		}
	}
}

// readNotifications reads lines from stdout and sends notifications on the
// provided channel until a TurnCompleted notification is seen.
// It blocks until the turn is done, an error occurs, or the process dies.
// Returns the accumulated notifications.
func (s *appServer) readNotifications(ch chan<- notification) error {
	for {
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return fmt.Errorf("read notification: %w", err)
			}
			return fmt.Errorf("app-server stdout closed")
		}

		line := s.scanner.Text()
		if line == "" {
			continue
		}

		if s.debug {
			log.Printf("[codex-cli] ← %s", truncateLog(line, 200))
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		// Only handle notifications here.
		if resp.ID != nil || resp.Method == "" {
			continue
		}

		n := notification{Method: resp.Method, Params: resp.Params}
		ch <- n

		// TurnCompleted signals end of turn.
		if resp.Method == "notifications/turnCompleted" {
			return nil
		}

		// Error notification also terminates.
		if resp.Method == "notifications/error" {
			return nil
		}
	}
}

func truncateLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
