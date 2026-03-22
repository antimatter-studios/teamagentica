package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CertManager handles CA operations and certificate generation for mTLS.
type CertManager struct {
	certsDir string
	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey
}

// NewCertManager initializes the cert manager. Creates CA if it doesn't exist.
func NewCertManager(dataDir string) (*CertManager, error) {
	certsDir := filepath.Join(dataDir, "certs")
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}

	cm := &CertManager{certsDir: certsDir}

	caCertPath := filepath.Join(certsDir, "ca.crt")
	caKeyPath := filepath.Join(certsDir, "ca.key")

	// If CA files already exist, load them.
	if fileExists(caCertPath) && fileExists(caKeyPath) {
		if err := cm.loadCA(caCertPath, caKeyPath); err != nil {
			return nil, fmt.Errorf("load existing CA: %w", err)
		}
		return cm, nil
	}

	// Otherwise, generate a new CA.
	if err := cm.InitCA(); err != nil {
		return nil, fmt.Errorf("init CA: %w", err)
	}

	return cm, nil
}

// InitCA generates a new CA certificate and key.
func (cm *CertManager) InitCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "TeamAgentica CA",
			Organization: []string{"TeamAgentica"},
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}

	// Save to disk.
	caCertPath := filepath.Join(cm.certsDir, "ca.crt")
	caKeyPath := filepath.Join(cm.certsDir, "ca.key")

	if err := writeCertPEM(caCertPath, certDER); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := writeKeyPEM(caKeyPath, key); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}

	cm.caCert = cert
	cm.caKey = key
	return nil
}

// GeneratePluginCert creates a cert for a plugin, signed by the CA.
func (cm *CertManager) GeneratePluginCert(pluginID string) (certPath, keyPath, caPath string, err error) {
	pluginDir := filepath.Join(cm.certsDir, pluginID)
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		return "", "", "", fmt.Errorf("create plugin cert dir: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", fmt.Errorf("generate plugin key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return "", "", "", err
	}

	containerName := "teamagentica-plugin-" + pluginID

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   pluginID,
			Organization: []string{"TeamAgentica Plugin"},
		},
		DNSNames: []string{
			pluginID,
			containerName,
			"localhost",
		},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, cm.caCert, &key.PublicKey, cm.caKey)
	if err != nil {
		return "", "", "", fmt.Errorf("create plugin cert: %w", err)
	}

	certPath = filepath.Join(pluginDir, pluginID+".crt")
	keyPath = filepath.Join(pluginDir, pluginID+".key")
	caPath = filepath.Join(pluginDir, "ca.crt")

	if err := writeCertPEM(certPath, certDER); err != nil {
		return "", "", "", fmt.Errorf("write plugin cert: %w", err)
	}
	if err := writeKeyPEM(keyPath, key); err != nil {
		return "", "", "", fmt.Errorf("write plugin key: %w", err)
	}

	// Copy CA cert into the plugin's directory so it can verify the kernel.
	caCertPEM, err := os.ReadFile(filepath.Join(cm.certsDir, "ca.crt"))
	if err != nil {
		return "", "", "", fmt.Errorf("read CA cert: %w", err)
	}
	if err := os.WriteFile(caPath, caCertPEM, 0644); err != nil {
		return "", "", "", fmt.Errorf("copy CA cert to plugin dir: %w", err)
	}

	return certPath, keyPath, caPath, nil
}

// GenerateKernelCert creates a cert for the kernel itself, signed by the CA.
func (cm *CertManager) GenerateKernelCert() (certPath, keyPath string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate kernel key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "teamagentica-kernel",
			Organization: []string{"TeamAgentica"},
		},
		DNSNames: []string{
			"kernel",
			"localhost",
			"teamagentica-kernel",
			"teamagentica-kernel",
		},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("0.0.0.0"),
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, cm.caCert, &key.PublicKey, cm.caKey)
	if err != nil {
		return "", "", fmt.Errorf("create kernel cert: %w", err)
	}

	certPath = filepath.Join(cm.certsDir, "kernel.crt")
	keyPath = filepath.Join(cm.certsDir, "kernel.key")

	if err := writeCertPEM(certPath, certDER); err != nil {
		return "", "", fmt.Errorf("write kernel cert: %w", err)
	}
	if err := writeKeyPEM(keyPath, key); err != nil {
		return "", "", fmt.Errorf("write kernel key: %w", err)
	}

	return certPath, keyPath, nil
}

// GetServerTLSConfig returns a tls.Config for the kernel's HTTPS server.
//
// SECURITY NOTE: This uses VerifyClientCertIfGiven (not RequireAndVerifyClientCert)
// because the same Gin router serves both the HTTP (8080) and TLS (8081) ports.
// Unauthenticated endpoints on the TLS port include:
//   - /api/health           — Docker/orchestrator health checks
//   - /api/webhook/:id/*    — external webhook ingress (Telegram, Discord, etc.)
//   - /ws/:container_id/*   — workspace proxy (unguessable IDs)
//
// RequireAndVerifyClientCert would break these endpoints on port 8081.
//
// Plugin auth is enforced at the middleware layer: PluginTokenAuth() requires
// either a valid mTLS client cert OR a valid JWT. On TLS connections, it rejects
// JWT-only auth (plugins MUST present their mTLS cert when connecting over TLS).
func (cm *CertManager) GetServerTLSConfig() (*tls.Config, error) {
	certPath := filepath.Join(cm.certsDir, "kernel.crt")
	keyPath := filepath.Join(cm.certsDir, "kernel.key")

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load kernel cert: %w", err)
	}

	caPool, err := cm.loadCAPool()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetClientTLSConfig returns a tls.Config for making requests to plugins.
// The kernel presents its own cert as a client certificate.
func (cm *CertManager) GetClientTLSConfig() (*tls.Config, error) {
	certPath := filepath.Join(cm.certsDir, "kernel.crt")
	keyPath := filepath.Join(cm.certsDir, "kernel.key")

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load kernel client cert: %w", err)
	}

	caPool, err := cm.loadCAPool()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetPluginCertDir returns the directory where a plugin's certs are stored.
// This is the host path that gets volume-mounted into the plugin container.
func (cm *CertManager) GetPluginCertDir(pluginID string) string {
	return filepath.Join(cm.certsDir, pluginID) + "/"
}

// --- helpers ---

func (cm *CertManager) loadCA(certPath, keyPath string) error {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read CA cert: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("no PEM block in CA cert")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read CA key: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("no PEM block in CA key")
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA key: %w", err)
	}

	cm.caCert = cert
	cm.caKey = key
	return nil
}

func (cm *CertManager) loadCAPool() (*x509.CertPool, error) {
	caCertPEM, err := os.ReadFile(filepath.Join(cm.certsDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to add CA cert to pool")
	}
	return pool, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

func writeCertPEM(path string, certDER []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal EC key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
