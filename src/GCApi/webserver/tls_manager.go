package webserver

import (
	"crypto/tls"
	"log"
	"os"
	"sync"
	"time"
)

type TLSReloader struct {
	certFile    string
	keyFile     string
	cert        *tls.Certificate
	mu          sync.RWMutex
	lastModCert time.Time
	lastModKey  time.Time
}

func NewTLSReloader(certFile, keyFile string) (*TLSReloader, error) {
	reloader := &TLSReloader{
		certFile: certFile,
		keyFile:  keyFile,
	}

	if err := reloader.reload(); err != nil {
		return nil, err
	}

	// Start watching for changes
	go reloader.watchFiles()

	return reloader, nil
}

func (r *TLSReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.cert = &cert
	r.mu.Unlock()

	// Update modification times
	certInfo, _ := os.Stat(r.certFile)
	keyInfo, _ := os.Stat(r.keyFile)

	if certInfo != nil {
		r.lastModCert = certInfo.ModTime()
	}
	if keyInfo != nil {
		r.lastModKey = keyInfo.ModTime()
	}

	log.Printf("TLS certificates reloaded")
	return nil
}

func (r *TLSReloader) watchFiles() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		certInfo, err := os.Stat(r.certFile)
		if err != nil {
			log.Printf("Failed to stat cert file: %v", err)
			continue
		}

		keyInfo, err := os.Stat(r.keyFile)
		if err != nil {
			log.Printf("Failed to stat key file: %v", err)
			continue
		}

		// Check if files have been modified
		if certInfo.ModTime().After(r.lastModCert) || keyInfo.ModTime().After(r.lastModKey) {
			log.Printf("Certificate files changed, reloading...")
			if err := r.reload(); err != nil {
				log.Printf("Failed to reload certificates: %v", err)
			}
		}
	}
}

func (r *TLSReloader) GetCertificate() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.cert, nil
	}
}

func (r *TLSReloader) GetConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: r.GetCertificate(),
		MinVersion:     tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}
}
