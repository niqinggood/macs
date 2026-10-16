package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// EnsureTLSCert loads or creates the self-signed certificate used by the local
// HTTPS endpoint.
func EnsureTLSCert(workspace string) (*tls.Config, error) {
	dir := filepath.Join(workspace, ".openkitty")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "openkitty", Organization: []string{"OpenKitty"}},
		NotBefore:    now,
		NotAfter:     now.Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	log.Printf("[TLS] Generated new self-signed certificate")
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

// StartHTTPSServer runs the optional local HTTPS endpoint.
func StartHTTPSServer(workspace, address string, handler http.Handler) {
	tlsConfig, err := EnsureTLSCert(workspace)
	if err != nil {
		log.Printf("[TLS] Cert generation failed, HTTPS unavailable: %v", err)
		return
	}
	server := &http.Server{Addr: address, Handler: handler, TLSConfig: tlsConfig}
	log.Printf("[TLS] HTTPS server on %s (self-signed cert, local use only)", address)
	if err := server.ListenAndServeTLS("", ""); err != nil {
		log.Printf("[TLS] HTTPS server stopped: %v", err)
	}
}
