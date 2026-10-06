package transport

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomerCATrustAndHostnameVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer server.Close()
	cert := server.Certificate()
	if !cert.IsCA {
		t.Fatal("test server certificate is not a CA")
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	untrusted, _, err := New(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if response, err := untrusted.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("untrusted private certificate accepted")
	}
	trusted, digest, err := New(nil, path)
	if err != nil || len(digest) != 64 {
		t.Fatalf("customer trust: %s, %v", digest, err)
	}
	response, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	// DNS localhost is not one of this CA's IP/DNS names. Certificate trust
	// must not disable hostname validation.
	if response, err := trusted.Get(strings.Replace(server.URL, "127.0.0.1", "localhost", 1)); err == nil {
		response.Body.Close()
		t.Fatal("wrong hostname was accepted")
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, nextDigest, err := New(nil, path)
	if err != nil || nextDigest == digest {
		t.Fatal("mutable CA file did not change trust identity")
	}
	global := http.DefaultTransport.(*http.Transport).TLSClientConfig
	if global != nil && (global.RootCAs != nil || global.InsecureSkipVerify) {
		t.Fatal("mutated global transport")
	}
	for _, contents := range [][]byte{nil, []byte("invalid"), append([]byte("garbage\n"), data...), append(data, []byte("garbage")...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-key")}), []byte(strings.Repeat("a", maxCABundleBytes+1))} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := New(nil, path); err == nil {
			t.Fatal("accepted invalid CA bundle")
		}
	}
	if _, _, err := New(nil, path+"missing"); err == nil {
		t.Fatal("accepted missing CA")
	}
	for _, cfg := range []*tls.Config{{InsecureSkipVerify: true}, {ServerName: "substitute.test"}} {
		base := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		if _, _, err := New(base, ""); err == nil {
			t.Fatal("accepted insecure transport")
		}
	}
}
