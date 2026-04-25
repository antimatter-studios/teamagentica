// Package sshreverse implements a reverse SSH port-forward tunnel driver.
//
// It connects out to a user-provided public SSH bastion and asks that bastion
// to listen on a remote port; incoming connections on that port are piped
// back over the SSH session to a local target (the tunnel's `target` field).
//
// This mirrors the role ngrok plays, but over vanilla SSH — useful when the
// user owns a public VPS with sshd and doesn't want to depend on ngrok.
package sshreverse

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

const ID = "ssh-reverse"

func init() {
	drivers.Register(ID, New)
}

// cfg keys.
const (
	keyHost           = "host"
	keyPort           = "port"
	keyUser           = "user"
	keyPrivateKey     = "private_key"
	keyPassword       = "password"
	keyRemoteBindHost = "remote_bind_host"
	keyRemoteBindPort = "remote_bind_port"
	keyKnownHosts     = "known_hosts" // authorized_keys-style list; if empty, InsecureIgnoreHostKey
)

type driver struct {
	target     string
	host       string
	port       int
	user       string
	authMethod ssh.AuthMethod
	hostKeys   []ssh.PublicKey // empty => accept any
	remoteHost string
	remotePort int

	mu       sync.RWMutex
	cancel   context.CancelFunc
	client   *ssh.Client
	listener net.Listener
	state    string
	url      string
	errs     string
}

// New builds an ssh-reverse driver. Required: host, user, and either
// private_key or password. remote_bind_port is required if you want a stable
// public port; defaults to 0 (bastion-assigned).
func New(target string, cfg map[string]string) (drivers.Driver, error) {
	if target == "" {
		return nil, fmt.Errorf("ssh-reverse: target required")
	}
	host := cfg[keyHost]
	if host == "" {
		return nil, fmt.Errorf("ssh-reverse: host required")
	}
	user := cfg[keyUser]
	if user == "" {
		return nil, fmt.Errorf("ssh-reverse: user required")
	}
	port, err := parsePort(cfg[keyPort], 22)
	if err != nil {
		return nil, fmt.Errorf("ssh-reverse: port: %w", err)
	}
	remotePort, err := parsePort(cfg[keyRemoteBindPort], 0)
	if err != nil {
		return nil, fmt.Errorf("ssh-reverse: remote_bind_port: %w", err)
	}
	remoteHost := cfg[keyRemoteBindHost]
	if remoteHost == "" {
		remoteHost = "0.0.0.0"
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return nil, err
	}

	hostKeys, err := parseKnownHosts(cfg[keyKnownHosts])
	if err != nil {
		return nil, fmt.Errorf("ssh-reverse: known_hosts: %w", err)
	}

	return &driver{
		target:     target,
		host:       host,
		port:       port,
		user:       user,
		authMethod: auth,
		hostKeys:   hostKeys,
		remoteHost: remoteHost,
		remotePort: remotePort,
		state:      drivers.StateStopped,
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
	d.url = fmt.Sprintf("ssh://%s@%s:%d (remote bind %s:%d -> %s)", d.user, d.host, d.port, d.remoteHost, d.remotePort, d.target)
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
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
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
	return drivers.Status{State: d.state, URL: d.url, Error: d.errs}
}

func (d *driver) setError(msg string) {
	d.mu.Lock()
	d.state = drivers.StateError
	d.errs = msg
	d.mu.Unlock()
}

func (d *driver) dialAndListen(ctx context.Context) (*ssh.Client, net.Listener, error) {
	cfg := &ssh.ClientConfig{
		User:            d.user,
		Auth:            []ssh.AuthMethod{d.authMethod},
		HostKeyCallback: d.hostKeyCallback(),
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(d.host, strconv.Itoa(d.port))
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

	listenAddr := net.JoinHostPort(d.remoteHost, strconv.Itoa(d.remotePort))
	listener, err := client.Listen("tcp", listenAddr)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("remote listen %s: %w", listenAddr, err)
	}
	return client, listener, nil
}

func (d *driver) acceptLoop(ctx context.Context, listener net.Listener) {
	for {
		remote, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[ssh-reverse] accept: %v", err)
			return
		}
		go d.proxy(remote)
	}
}

func (d *driver) proxy(remote net.Conn) {
	defer remote.Close()
	local, err := net.Dial("tcp", d.target)
	if err != nil {
		log.Printf("[ssh-reverse] dial target %s: %v", d.target, err)
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	<-done
}

func (d *driver) hostKeyCallback() ssh.HostKeyCallback {
	if len(d.hostKeys) == 0 {
		return ssh.InsecureIgnoreHostKey()
	}
	allowed := d.hostKeys
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		for _, k := range allowed {
			if string(k.Marshal()) == string(key.Marshal()) {
				return nil
			}
		}
		return fmt.Errorf("host key for %s not in known_hosts", hostname)
	}
}

func buildAuth(cfg map[string]string) (ssh.AuthMethod, error) {
	if pem := cfg[keyPrivateKey]; pem != "" {
		signer, err := ssh.ParsePrivateKey([]byte(pem))
		if err != nil {
			return nil, fmt.Errorf("ssh-reverse: parse private_key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}
	if pw := cfg[keyPassword]; pw != "" {
		return ssh.Password(pw), nil
	}
	return nil, fmt.Errorf("ssh-reverse: either private_key or password is required")
}

// parseKnownHosts accepts one-key-per-line in SSH authorized_keys format.
// Empty input means no host-key pinning (insecure).
func parseKnownHosts(raw string) ([]ssh.PublicKey, error) {
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
