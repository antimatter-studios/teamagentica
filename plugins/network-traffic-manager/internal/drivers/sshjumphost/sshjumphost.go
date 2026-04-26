// Package sshjumphost implements an SSH jumphost tunnel driver.
//
// It opens a reverse SSH tunnel to a public bastion (same mechanism as the
// ssh-reverse driver), but instead of TCP-piping inbound conns to a backend
// it terminates SSH locally with an embedded SSH server. Authenticated
// clients that request agent-forwarding (`ssh -A`) get their forwarded
// ssh-agent exposed at a configured Unix socket path on the local
// filesystem; that socket is intended to be picked up by a workspace
// container via SSH_AUTH_SOCK.
//
// Architecture:
//
//	macbook (ssh-agent) → ssh -A user@bastion:port
//	        ↓
//	bastion:port [reverse tunnel from traffic-manager]
//	        ↓
//	traffic-manager: this driver receives the SSH session, validates
//	pubkey, accepts the auth-agent-req, exposes the agent over a unix
//	socket at <agent_socket_path>.
//
// The workspace is a passive consumer; this driver owns ALL tunnel and
// SSH-server mechanics.
package sshjumphost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

const ID = "ssh-jumphost"

func init() {
	drivers.Register(ID, New)
}

// cfg keys.
const (
	keyBastionHost           = "bastion_host"
	keyBastionPort           = "bastion_port"
	keyBastionUser           = "bastion_user"
	keyBastionPrivateKey     = "bastion_private_key"
	keyBastionKnownHosts     = "bastion_known_hosts"
	keyBastionRemoteBindHost = "bastion_remote_bind_host"
	keyBastionRemoteBindPort = "bastion_remote_bind_port"

	keyUsername        = "username"
	keyAuthorizedKeys  = "authorized_keys"
	keyAgentSocketPath = "agent_socket_path"
	keyHostKey         = "host_key"
	keyHostKeyPath     = "host_key_path"
	keySocketMode      = "socket_mode"
)

const (
	agentChannelType = "auth-agent@openssh.com"
	agentReqType     = "auth-agent-req@openssh.com"
)

type driver struct {
	// bastion (outbound reverse tunnel) config
	bastionHost       string
	bastionPort       int
	bastionUser       string
	bastionAuth       ssh.AuthMethod
	bastionHostKeys   []ssh.PublicKey // empty => accept any
	bastionRemoteHost string
	bastionRemotePort int

	// embedded SSH server config
	username       string
	authorizedKeys []ssh.PublicKey
	hostSigner     ssh.Signer
	socketPath     string
	socketMode     os.FileMode

	mu       sync.RWMutex
	cancel   context.CancelFunc
	client   *ssh.Client
	listener net.Listener
	state    string
	url      string
	errs     string

	// active SSH conns (for clean Stop)
	connsMu sync.Mutex
	conns   map[*ssh.ServerConn]struct{}
	// active socket listeners keyed by ssh conn (so we can close on session end)
	socketListenersMu sync.Mutex
	socketListeners   map[*ssh.ServerConn]net.Listener
}

// New builds an ssh-jumphost driver.
//
// The `target` argument is unused — for this driver the conceptual "target"
// is the agent socket path, which is supplied via the agent_socket_path cfg
// key. The Factory signature requires target, so we accept-and-ignore.
func New(_ /* name */ string, target string, cfg map[string]string) (drivers.Driver, error) {
	_ = target // intentionally unused; see doc above

	host := cfg[keyBastionHost]
	if host == "" {
		return nil, fmt.Errorf("ssh-jumphost: bastion_host required")
	}
	user := cfg[keyBastionUser]
	if user == "" {
		return nil, fmt.Errorf("ssh-jumphost: bastion_user required")
	}
	pkPEM := cfg[keyBastionPrivateKey]
	if pkPEM == "" {
		return nil, fmt.Errorf("ssh-jumphost: bastion_private_key required")
	}
	username := cfg[keyUsername]
	if username == "" {
		return nil, fmt.Errorf("ssh-jumphost: username required")
	}
	authorizedRaw := cfg[keyAuthorizedKeys]
	if authorizedRaw == "" {
		return nil, fmt.Errorf("ssh-jumphost: authorized_keys required")
	}
	socketPath := cfg[keyAgentSocketPath]
	if socketPath == "" {
		return nil, fmt.Errorf("ssh-jumphost: agent_socket_path required")
	}

	port, err := parsePort(cfg[keyBastionPort], 22)
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: bastion_port: %w", err)
	}
	remotePort, err := parsePort(cfg[keyBastionRemoteBindPort], 0)
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: bastion_remote_bind_port: %w", err)
	}
	remoteHost := cfg[keyBastionRemoteBindHost]
	if remoteHost == "" {
		remoteHost = "0.0.0.0"
	}

	bastionSigner, err := ssh.ParsePrivateKey([]byte(pkPEM))
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: parse bastion_private_key: %w", err)
	}

	hostKeys, err := parseAuthorizedKeys(cfg[keyBastionKnownHosts])
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: bastion_known_hosts: %w", err)
	}

	authKeys, err := parseAuthorizedKeys(authorizedRaw)
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: authorized_keys: %w", err)
	}
	if len(authKeys) == 0 {
		return nil, fmt.Errorf("ssh-jumphost: authorized_keys parsed empty")
	}

	hostSigner, err := buildHostSigner(cfg[keyHostKey], cfg[keyHostKeyPath])
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: host_key: %w", err)
	}

	socketMode, err := parseFileMode(cfg[keySocketMode], 0o666)
	if err != nil {
		return nil, fmt.Errorf("ssh-jumphost: socket_mode: %w", err)
	}

	return &driver{
		bastionHost:       host,
		bastionPort:       port,
		bastionUser:       user,
		bastionAuth:       ssh.PublicKeys(bastionSigner),
		bastionHostKeys:   hostKeys,
		bastionRemoteHost: remoteHost,
		bastionRemotePort: remotePort,

		username:       username,
		authorizedKeys: authKeys,
		hostSigner:     hostSigner,
		socketPath:     socketPath,
		socketMode:     socketMode,

		state:           drivers.StateStopped,
		conns:           make(map[*ssh.ServerConn]struct{}),
		socketListeners: make(map[*ssh.ServerConn]net.Listener),
	}, nil
}

func (d *driver) Start(ctx context.Context) (drivers.Status, error) {
	d.mu.Lock()
	if d.client != nil {
		st := d.statusLocked()
		d.mu.Unlock()
		return st, nil
	}
	d.state = drivers.StateStarting
	d.errs = ""
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.mu.Unlock()

	client, listener, err := d.dialAndListen(ctx)
	if err != nil {
		d.setError(err.Error())
		cancel()
		return d.Status(), err
	}

	d.mu.Lock()
	d.client = client
	d.listener = listener
	d.url = fmt.Sprintf("ssh-jumphost://%s:%d remote_bind=%s:%d sock=%s",
		d.bastionHost, d.bastionPort, d.bastionRemoteHost, d.bastionRemotePort, d.socketPath)
	d.state = drivers.StateRunning
	st := d.statusLocked()
	d.mu.Unlock()

	go d.acceptLoop(ctx, listener)
	return st, nil
}

func (d *driver) Stop(ctx context.Context) error {
	d.mu.Lock()
	cancel := d.cancel
	client := d.client
	listener := d.listener
	d.client = nil
	d.listener = nil
	d.cancel = nil
	d.state = drivers.StateStopped
	d.url = ""
	socketPath := d.socketPath
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}

	// close every active SSH conn
	d.connsMu.Lock()
	conns := make([]*ssh.ServerConn, 0, len(d.conns))
	for c := range d.conns {
		conns = append(conns, c)
	}
	d.conns = make(map[*ssh.ServerConn]struct{})
	d.connsMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}

	// close every active socket listener and remove the file
	d.socketListenersMu.Lock()
	socketListeners := make([]net.Listener, 0, len(d.socketListeners))
	for _, l := range d.socketListeners {
		socketListeners = append(socketListeners, l)
	}
	d.socketListeners = make(map[*ssh.ServerConn]net.Listener)
	d.socketListenersMu.Unlock()
	for _, l := range socketListeners {
		_ = l.Close()
	}
	if socketPath != "" {
		_ = os.Remove(socketPath)
	}

	if client != nil {
		return client.Close()
	}
	return nil
}

func (d *driver) Status() drivers.Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.statusLocked()
}

func (d *driver) statusLocked() drivers.Status {
	return drivers.Status{State: d.state, URL: d.url, Error: d.errs, LocalSocketPath: d.socketPath}
}

func (d *driver) setError(msg string) {
	d.mu.Lock()
	d.state = drivers.StateError
	d.errs = msg
	d.mu.Unlock()
}

// dialAndListen mirrors sshreverse: dial bastion, ask for a remote listener.
func (d *driver) dialAndListen(ctx context.Context) (*ssh.Client, net.Listener, error) {
	cfg := &ssh.ClientConfig{
		User:            d.bastionUser,
		Auth:            []ssh.AuthMethod{d.bastionAuth},
		HostKeyCallback: d.bastionHostKeyCallback(),
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(d.bastionHost, strconv.Itoa(d.bastionPort))
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	listenAddr := net.JoinHostPort(d.bastionRemoteHost, strconv.Itoa(d.bastionRemotePort))
	listener, err := client.Listen("tcp", listenAddr)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("remote listen %s: %w", listenAddr, err)
	}
	return client, listener, nil
}

func (d *driver) acceptLoop(ctx context.Context, listener net.Listener) {
	defer recoverPanic("acceptLoop")
	for {
		remote, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[ssh-jumphost] accept: %v", err)
			return
		}
		go d.handleSSHConn(ctx, remote)
	}
}

func (d *driver) handleSSHConn(ctx context.Context, raw net.Conn) {
	defer recoverPanic("handleSSHConn")
	defer raw.Close()

	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() != d.username {
				return nil, fmt.Errorf("user %q not allowed", meta.User())
			}
			marshaled := key.Marshal()
			for _, ak := range d.authorizedKeys {
				if string(ak.Marshal()) == string(marshaled) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, fmt.Errorf("pubkey not authorized")
		},
	}
	serverCfg.AddHostKey(d.hostSigner)

	sshConn, chans, reqs, err := ssh.NewServerConn(raw, serverCfg)
	if err != nil {
		log.Printf("[ssh-jumphost] handshake: %v", err)
		return
	}

	d.connsMu.Lock()
	d.conns[sshConn] = struct{}{}
	d.connsMu.Unlock()

	defer func() {
		d.connsMu.Lock()
		delete(d.conns, sshConn)
		d.connsMu.Unlock()
		_ = sshConn.Close()
	}()

	// drop global requests
	go ssh.DiscardRequests(reqs)

	// handle session channels (others rejected)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			log.Printf("[ssh-jumphost] accept session: %v", err)
			continue
		}
		go d.handleSession(ctx, sshConn, ch, chReqs)
	}
}

func (d *driver) handleSession(ctx context.Context, sshConn *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer recoverPanic("handleSession")
	defer ch.Close()

	agentForwarding := false
	var socketListener net.Listener
	defer func() {
		if socketListener != nil {
			d.socketListenersMu.Lock()
			delete(d.socketListeners, sshConn)
			d.socketListenersMu.Unlock()
			_ = socketListener.Close()
			_ = os.Remove(d.socketPath)
		}
	}()

	// also stop on context cancel
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = ch.Close()
		case <-doneCh:
		}
	}()

	for req := range reqs {
		switch req.Type {
		case agentReqType:
			if agentForwarding {
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				continue
			}
			lis, err := d.startAgentSocket(sshConn)
			if err != nil {
				log.Printf("[ssh-jumphost] start agent socket: %v", err)
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			socketListener = lis
			agentForwarding = true
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "shell", "exec":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			msg := "ssh-agent forwarding active. Ctrl-C to disconnect.\r\n"
			_, _ = io.WriteString(ch, msg)
		case "pty-req", "env", "window-change":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// startAgentSocket creates the unix socket at agent_socket_path and runs an
// accept-loop that pipes each socket conn to a fresh auth-agent@openssh.com
// channel back on the SSH conn.
func (d *driver) startAgentSocket(sshConn *ssh.ServerConn) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(d.socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parents: %w", err)
	}
	// Best-effort cleanup of any stale socket file.
	_ = os.Remove(d.socketPath)

	lis, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", d.socketPath, err)
	}
	if err := os.Chmod(d.socketPath, d.socketMode); err != nil {
		log.Printf("[ssh-jumphost] chmod socket %s: %v", d.socketPath, err)
	}

	d.socketListenersMu.Lock()
	d.socketListeners[sshConn] = lis
	d.socketListenersMu.Unlock()

	go func() {
		defer recoverPanic("agent-socket-acceptLoop")
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go d.proxyAgentConn(sshConn, c)
		}
	}()
	return lis, nil
}

// proxyAgentConn opens a fresh auth-agent@openssh.com channel on sshConn and
// pipes bytes between the unix-socket client and that channel.
func (d *driver) proxyAgentConn(sshConn *ssh.ServerConn, c net.Conn) {
	defer recoverPanic("proxyAgentConn")
	defer c.Close()

	ch, reqs, err := sshConn.OpenChannel(agentChannelType, nil)
	if err != nil {
		log.Printf("[ssh-jumphost] open %s channel: %v", agentChannelType, err)
		return
	}
	defer ch.Close()
	go ssh.DiscardRequests(reqs)

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(ch, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, ch); done <- struct{}{} }()
	<-done
}

func (d *driver) bastionHostKeyCallback() ssh.HostKeyCallback {
	if len(d.bastionHostKeys) == 0 {
		return ssh.InsecureIgnoreHostKey()
	}
	allowed := d.bastionHostKeys
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		for _, k := range allowed {
			if string(k.Marshal()) == string(key.Marshal()) {
				return nil
			}
		}
		return fmt.Errorf("host key for %s not in known_hosts", hostname)
	}
}

// parseAuthorizedKeys accepts one-key-per-line in SSH authorized_keys format.
func parseAuthorizedKeys(raw string) ([]ssh.PublicKey, error) {
	if raw == "" {
		return nil, nil
	}
	var out []ssh.PublicKey
	rest := []byte(raw)
	for len(rest) > 0 {
		pk, _, _, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, pk)
		rest = r
	}
	return out, nil
}

// buildHostSigner resolves the embedded SSH server's host key with the
// following precedence:
//  1. Inline PEM via host_key — use as-is.
//  2. Path via host_key_path — load if file exists; otherwise generate a fresh
//     ed25519 key and persist it (0600) at that path. Persistence keeps a
//     stable host identity across restarts so user known_hosts pinning works.
//  3. Neither set — generate ephemeral key, never persist (legacy v0 behavior).
func buildHostSigner(pemStr, path string) (ssh.Signer, error) {
	if pemStr != "" {
		signer, err := ssh.ParsePrivateKey([]byte(pemStr))
		if err != nil {
			return nil, fmt.Errorf("parse host_key: %w", err)
		}
		return signer, nil
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			signer, err := ssh.ParsePrivateKey(data)
			if err != nil {
				return nil, fmt.Errorf("parse host_key_path %s: %w", path, err)
			}
			return signer, nil
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read host_key_path %s: %w", path, err)
		}
		// Generate + persist.
		signer, encoded, err := generateEd25519Signer()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir host_key_path parents: %w", err)
		}
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			return nil, fmt.Errorf("write host_key_path %s: %w", path, err)
		}
		return signer, nil
	}
	// Neither set — ephemeral.
	signer, _, err := generateEd25519Signer()
	return signer, err
}

func generateEd25519Signer() (ssh.Signer, []byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal host key: %w", err)
	}
	encoded := pem.EncodeToMemory(block)
	signer, err := ssh.ParsePrivateKey(encoded)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated host key: %w", err)
	}
	return signer, encoded, nil
}

// parseFileMode parses an octal file mode string ("0666", "666", "0o666").
// Returns def when input is empty.
func parseFileMode(s string, def os.FileMode) (os.FileMode, error) {
	if s == "" {
		return def, nil
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0o"), "0O")
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(n), nil
}

func parsePort(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 0 || n > 65535 {
		return 0, fmt.Errorf("out of range: %d", n)
	}
	return n, nil
}

func recoverPanic(where string) {
	if r := recover(); r != nil {
		log.Printf("[ssh-jumphost] panic in %s: %v", where, r)
	}
}
