package codexcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync/atomic"
)

// appServer manages a persistent `codex app-server` subprocess.
type appServer struct {
	proc    *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	alive   bool
	nextID  atomic.Int64
	debug   bool
}

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCResponse is a raw incoming JSON-RPC message.
type jsonRPCResponse struct {
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

// start spawns the app-server process.
func (s *appServer) start(binary string, env []string) error {
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
	s.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	s.alive = true

	if s.debug {
		log.Printf("[codex-cli] app-server started (pid %d)", cmd.Process.Pid)
	}

	// Monitor process exit.
	go func() {
		err := cmd.Wait()
		s.alive = false
		if s.debug {
			log.Printf("[codex-cli] app-server exited: %v", err)
		}
	}()

	return nil
}

// sendRequest sends a JSON-RPC request and reads the response.
// Blocks until a response with matching ID is received.
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
		return nil, fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	if s.debug {
		log.Printf("[codex-cli] → %s (id=%d)", method, id)
	}

	if _, err := s.stdin.Write(data); err != nil {
		s.alive = false
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read until we get a response with our ID.
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		// Notification — skip for now.
		if resp.ID == nil {
			if s.debug {
				log.Printf("[codex-cli] ← notification: %s", resp.Method)
			}
			continue
		}

		if *resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			if s.debug {
				log.Printf("[codex-cli] ← response id=%d (%d bytes)", id, len(resp.Result))
			}
			return resp.Result, nil
		}
	}

	s.alive = false
	return nil, fmt.Errorf("app-server closed while waiting for response to %s", method)
}

// initialize sends the initialize handshake.
func (s *appServer) initialize() error {
	_, err := s.sendRequest("initialize", map[string]interface{}{
		"clientInfo": map[string]string{
			"name":    "teamagentica-agent-openai",
			"version": "1.0.0",
		},
	})
	return err
}

// stop kills the app-server process.
func (s *appServer) stop() {
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
