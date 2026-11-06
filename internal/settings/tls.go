package settings

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultTLSCertPath   = "/etc/vestri/certs/worker.crt"
	defaultTLSKeyPath    = "/etc/vestri/certs/worker.key"
	defaultTLSCACertPath = "/etc/vestri/certs/ca.crt"
	defaultTLSCAKeyPath  = "/etc/vestri/certs/ca.key"
)

const (
	autoTLSCACommonName     = "Vestri Worker Local CA"
	autoTLSServerCommonName = "vestri-worker"
)

func EnsureTLSAssets() (certPath, keyPath, caCertPath string, generated bool, err error) {
	cfg := Get()
	if !cfg.UseTLS {
		return strings.TrimSpace(cfg.TLSCert), strings.TrimSpace(cfg.TLSKey), strings.TrimSpace(cfg.TLSCACert), false, nil
	}

	certPath = nonEmptyOrDefault(cfg.TLSCert, defaultTLSCertPath)
	keyPath = nonEmptyOrDefault(cfg.TLSKey, defaultTLSKeyPath)
	caCertPath = nonEmptyOrDefault(cfg.TLSCACert, defaultTLSCACertPath)
	caKeyPath := nonEmptyOrDefault(cfg.TLSCAKey, defaultTLSCAKeyPath)

	cfg.TLSCert = certPath
	cfg.TLSKey = keyPath
	cfg.TLSCACert = caCertPath
	cfg.TLSCAKey = caKeyPath
	Set(cfg)

	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)
	if certExists && keyExists {
		if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
			return "", "", "", false, fmt.Errorf("invalid TLS certificate/key pair: %w", err)
		}
		return certPath, keyPath, caCertPath, false, nil
	}
	if !cfg.TLSAutoGenerate {
		return "", "", "", false, fmt.Errorf("TLS certificate or key missing and tls_auto_generate is disabled")
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return "", "", "", false, fmt.Errorf("failed to prepare cert directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(caCertPath), 0o700); err != nil {
		return "", "", "", false, fmt.Errorf("failed to prepare CA directory: %w", err)
	}

	caCert, caKey, _, err := ensureCertificateAuthority(caCertPath, caKeyPath)
	if err != nil {
		return "", "", "", false, err
	}
	if err := generateServerCertificate(certPath, keyPath, cfg, caCert, caKey); err != nil {
		return "", "", "", false, err
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		return "", "", "", false, fmt.Errorf("failed to load generated TLS certificate/key pair: %w", err)
	}

	return certPath, keyPath, caCertPath, true, nil
}

func ensureCertificateAuthority(certPath, keyPath string) (*x509.Certificate, crypto.PrivateKey, bool, error) {
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)
	if certExists && keyExists {
		cert, err := loadCertificateFromFile(certPath)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		if !cert.IsCA {
			return nil, nil, false, errors.New("configured CA certificate is not a CA")
		}

		key, err := loadPrivateKeyFromFile(keyPath)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to read CA private key: %w", err)
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, nil, false, errors.New("configured CA private key is not a signer")
		}
		matches, err := publicKeysEqual(cert.PublicKey, signer.Public())
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to validate CA key pair: %w", err)
		}
		if !matches {
			return nil, nil, false, errors.New("configured CA certificate and private key do not match")
		}
		return cert, key, false, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to generate CA private key: %w", err)
	}
	serial, err := randomSerialNumber()
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to generate CA serial: %w", err)
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   autoTLSCACommonName,
			Organization: []string{"Vestri"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to parse generated CA certificate: %w", err)
	}

	if err := writeCertificatePEM(certPath, derBytes); err != nil {
		return nil, nil, false, fmt.Errorf("failed to write CA certificate: %w", err)
	}
	if err := writePrivateKeyPEM(keyPath, key); err != nil {
		return nil, nil, false, fmt.Errorf("failed to write CA private key: %w", err)
	}

	return cert, key, true, nil
}

func generateServerCertificate(certPath, keyPath string, cfg Settings, caCert *x509.Certificate, caKey crypto.PrivateKey) error {
	signer, ok := caKey.(crypto.Signer)
	if !ok {
		return errors.New("CA key is not a signer")
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate server key: %w", err)
	}
	serial, err := randomSerialNumber()
	if err != nil {
		return fmt.Errorf("failed to generate server serial: %w", err)
	}

	dnsNames, ipAddrs := collectSANs(cfg)
	commonName := autoTLSServerCommonName
	if len(dnsNames) > 0 {
		commonName = dnsNames[0]
	} else if len(ipAddrs) > 0 {
		commonName = ipAddrs[0].String()
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Vestri"},
		},
		NotBefore:   now.Add(-1 * time.Hour),
		NotAfter:    now.AddDate(2, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		IPAddresses: ipAddrs,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, serverKey.Public(), signer)
	if err != nil {
		return fmt.Errorf("failed to create server certificate: %w", err)
	}
	if err := writeCertificatePEM(certPath, derBytes); err != nil {
		return fmt.Errorf("failed to write server certificate: %w", err)
	}
	if err := writePrivateKeyPEM(keyPath, serverKey); err != nil {
		return fmt.Errorf("failed to write server key: %w", err)
	}

	return nil
}

func collectSANs(cfg Settings) ([]string, []net.IP) {
	dnsSet := map[string]struct{}{}
	ipSet := map[string]net.IP{}

	addEntry := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if ip := net.ParseIP(value); ip != nil {
			ipSet[ip.String()] = ip
			return
		}
		if strings.ContainsAny(value, " \t\r\n") {
			return
		}
		dnsSet[strings.ToLower(strings.TrimSuffix(value, "."))] = struct{}{}
	}

	addEntry("localhost")
	addEntry("127.0.0.1")
	addEntry("::1")

	if host, err := os.Hostname(); err == nil {
		addEntry(host)
	}
	addEntry(cfg.WorkerName)

	for _, entry := range cfg.TLSSANs {
		addEntry(entry)
	}

	for _, ip := range localInterfaceIPs() {
		addEntry(ip.String())
	}

	dnsNames := make([]string, 0, len(dnsSet))
	for name := range dnsSet {
		dnsNames = append(dnsNames, name)
	}
	sort.Strings(dnsNames)

	ipNames := make([]string, 0, len(ipSet))
	for raw := range ipSet {
		ipNames = append(ipNames, raw)
	}
	sort.Strings(ipNames)

	ips := make([]net.IP, 0, len(ipNames))
	for _, raw := range ipNames {
		ips = append(ips, ipSet[raw])
	}

	return dnsNames, ips
}

func localInterfaceIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	seen := map[string]net.IP{}
	for _, addr := range addrs {
		var ip net.IP
		switch typed := addr.(type) {
		case *net.IPNet:
			ip = typed.IP
		case *net.IPAddr:
			ip = typed.IP
		default:
			continue
		}
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		seen[ip.String()] = ip
	}

	keys := make([]string, 0, len(seen))
	for raw := range seen {
		keys = append(keys, raw)
	}
	sort.Strings(keys)

	out := make([]net.IP, 0, len(keys))
	for _, raw := range keys {
		out = append(out, seen[raw])
	}
	return out
}

func writeCertificatePEM(path string, derBytes []byte) error {
	block := &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}
	return writePEMFile(path, block, 0o644)
}

func writePrivateKeyPEM(path string, key crypto.PrivateKey) error {
	derBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: derBytes}
	return writePEMFile(path, block, 0o600)
}

func writePEMFile(path string, block *pem.Block, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

func loadCertificateFromFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("certificate PEM block not found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadPrivateKeyFromFile(path string) (crypto.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key PEM block not found")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported private key format")
}

func publicKeysEqual(a, b interface{}) (bool, error) {
	left, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false, err
	}
	right, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}

func randomSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	if n.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return n, nil
}

func nonEmptyOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
