package sshreverse

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

func TestRegistered(t *testing.T) {
	found := false
	for _, id := range drivers.Available() {
		if id == ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ssh-reverse not registered; available: %v", drivers.Available())
	}
}

func TestNew_RequiresTarget(t *testing.T) {
	if _, err := New("", map[string]string{"host": "h", "user": "u", "password": "p"}); err == nil {
		t.Fatal("expected error when target missing")
	}
}

func TestNew_RequiresHost(t *testing.T) {
	if _, err := New("local:1", map[string]string{"user": "u", "password": "p"}); err == nil {
		t.Fatal("expected error when host missing")
	}
}

func TestNew_RequiresUser(t *testing.T) {
	if _, err := New("local:1", map[string]string{"host": "h", "password": "p"}); err == nil {
		t.Fatal("expected error when user missing")
	}
}

func TestNew_RequiresAuth(t *testing.T) {
	if _, err := New("local:1", map[string]string{"host": "h", "user": "u"}); err == nil {
		t.Fatal("expected error when neither private_key nor password provided")
	}
}

func TestNew_InvalidPort(t *testing.T) {
	if _, err := New("local:1", map[string]string{"host": "h", "user": "u", "password": "p", "port": "99999"}); err == nil {
		t.Fatal("expected port range error")
	}
}

func TestNew_PrivateKey(t *testing.T) {
	pemStr := generateTestPrivateKeyPEM(t)
	d, err := New("local:1", map[string]string{
		"host":             "h",
		"user":             "u",
		"private_key":      pemStr,
		"remote_bind_port": "9443",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st := d.Status(); st.State != drivers.StateStopped {
		t.Errorf("initial state=%q, want stopped", st.State)
	}
}

func TestNew_KnownHostsParse(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)
	d, err := New("local:1", map[string]string{
		"host":        "h",
		"user":        "u",
		"password":    "pw",
		"known_hosts": pubPEM,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("nil driver")
	}
}

func TestStop_Idempotent(t *testing.T) {
	d, err := New("local:1", map[string]string{"host": "h", "user": "u", "password": "pw"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Errorf("Stop never-started: %v", err)
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		in      string
		def     int
		want    int
		wantErr bool
	}{
		{"", 22, 22, false},
		{"2222", 22, 2222, false},
		{"-1", 22, 0, true},
		{"70000", 22, 0, true},
		{"abc", 22, 0, true},
	}
	for _, c := range cases {
		got, err := parsePort(c.in, c.def)
		if (err != nil) != c.wantErr {
			t.Errorf("parsePort(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parsePort(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func generateTestKeyPair(t *testing.T) (privPEM, pubAuthorized string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal priv: %v", err)
	}
	privPEM = string(pem.EncodeToMemory(privBytes))

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh pub: %v", err)
	}
	pubAuthorized = string(ssh.MarshalAuthorizedKey(sshPub))
	return
}

func generateTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	priv, _ := generateTestKeyPair(t)
	return priv
}
