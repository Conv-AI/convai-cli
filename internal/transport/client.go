// Package transport constructs profile-local TLS trust without changing the
// process-wide transport or disabling certificate and hostname verification.
package transport

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const maxCABundleBytes = 1 << 20

// New returns an isolated HTTP client and the digest of the explicitly supplied
// CA bytes. That digest binds a managed session to trust at login, not merely
// to a mutable filename. System trust remains the OS administrator's authority.
func New(base *http.Client, caBundle string) (*http.Client, string, error) {
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	baseTransport := base.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	standard, ok := baseTransport.(*http.Transport)
	if !ok {
		if caBundle != "" {
			return nil, "", errors.New("customer CA requires a standard HTTP transport")
		}
		return &client, "", nil
	}
	tr := standard.Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.ServerName != "" {
		return nil, "", errors.New("profile TLS must verify certificates and the requested hostname")
	}
	client.Transport = tr
	if caBundle == "" {
		return &client, "", nil
	}
	file, err := os.Open(caBundle)
	if err != nil {
		return nil, "", fmt.Errorf("read customer CA bundle: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("customer CA bundle must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCABundleBytes+1))
	if err != nil || len(data) > maxCABundleBytes {
		return nil, "", errors.New("customer CA bundle cannot be read or exceeds 1 MiB")
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, "", fmt.Errorf("load system certificate trust: %w", err)
	}
	if tr.TLSClientConfig.RootCAs != nil {
		pool = tr.TLSClientConfig.RootCAs.Clone()
	}
	rest, count := data, 0
	for len(rest) > 0 {
		rest = bytes.TrimSpace(rest)
		if len(rest) == 0 {
			break
		}
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, "", errors.New("customer CA bundle must contain only PEM CA certificates")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, "", errors.New("customer CA bundle must contain only PEM CA certificates")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return nil, "", errors.New("customer CA bundle contains an invalid CA certificate")
		}
		pool.AddCert(cert)
		count++
		rest = remaining
	}
	if count == 0 {
		return nil, "", errors.New("customer CA bundle is empty")
	}
	tr.TLSClientConfig.RootCAs = pool
	digest := sha256.Sum256(data)
	return &client, hex.EncodeToString(digest[:]), nil
}
