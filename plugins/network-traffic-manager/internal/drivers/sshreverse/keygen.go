package sshreverse

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// keyDir is the on-disk location for auto-generated tunnel keys. One file per
// tunnel name. The plugin's /data is persistent across restarts.
const keyDir = "/data/ssh-reverse-keys"

// loadOrCreateKey returns an ssh.Signer + authorized_keys-format public key
// for the named tunnel. If a key file already exists at <keyDir>/<name>.pem
// it is loaded; otherwise a fresh ed25519 keypair is generated and persisted.
func loadOrCreateKey(name string) (ssh.Signer, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("name required")
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("mkdir %s: %w", keyDir, err)
	}
	path := filepath.Join(keyDir, sanitize(name)+".pem")

	if data, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, "", fmt.Errorf("parse stored key %s: %w", path, err)
		}
		return signer, pubKeyLine(signer.PublicKey(), name), nil
	} else if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate ed25519: %w", err)
	}
	pemBytes, err := marshalEd25519PEM(priv)
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, "", fmt.Errorf("write %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, "", fmt.Errorf("re-parse generated key: %w", err)
	}
	_ = pub
	return signer, pubKeyLine(signer.PublicKey(), name), nil
}

func pubKeyLine(pub ssh.PublicKey, name string) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " teamagentica:" + name
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// marshalEd25519PEM returns an OpenSSH-format PEM for an ed25519 private key
// that x/crypto/ssh.ParsePrivateKey can round-trip.
func marshalEd25519PEM(priv ed25519.PrivateKey) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal ed25519: %w", err)
	}
	return pem.EncodeToMemory(block), nil
}
