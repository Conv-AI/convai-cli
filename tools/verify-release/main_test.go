package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArchiveValidation(t *testing.T) {
	for _, zipFormat := range []bool{false, true} {
		valid := []string{"convai", "LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.txt"}
		for _, test := range []struct {
			names []string
			empty string
			valid bool
		}{
			{names: valid, valid: true},
			{names: append(slices.Clone(valid), "README.md"), valid: true},
			{names: []string{"convai"}},
			{names: []string{"convai", "NOTICE", "THIRD_PARTY_NOTICES.txt"}},
			{names: []string{"convai", "LICENSE", "THIRD_PARTY_NOTICES.txt"}},
			{names: []string{"convai", "LICENSE", "NOTICE"}},
			{names: valid, empty: "LICENSE"},
			{names: valid, empty: "NOTICE"},
			{names: valid, empty: "THIRD_PARTY_NOTICES.txt"},
			{names: []string{"../convai"}},
			{names: []string{"config.yaml"}},
			{names: append(slices.Clone(valid), "convai")},
			{names: append(slices.Clone(valid), "NOTICE")},
			{names: append(slices.Clone(valid), ".env")},
		} {
			var buffer bytes.Buffer
			fileName := "test.tar.gz"
			if zipFormat {
				fileName = "test.zip"
				writer := zip.NewWriter(&buffer)
				for _, name := range test.names {
					content := []byte("binary")
					if name == test.empty {
						content = []byte(" \n")
					}
					if name == "convai" {
						name += ".exe"
					}
					stream, err := writer.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = stream.Write(content)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				compressed := gzip.NewWriter(&buffer)
				writer := tar.NewWriter(compressed)
				for _, name := range test.names {
					content := []byte("binary")
					if name == test.empty {
						content = []byte(" \n")
					}
					if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
						t.Fatal(err)
					}
					_, _ = writer.Write(content)
				}
				_ = writer.Close()
				_ = compressed.Close()
			}
			_, err := readArchive(fileName, buffer.Bytes())
			if (err == nil) != test.valid {
				t.Fatalf("archive %v, empty %s, zip %v: %v", test.names, test.empty, zipFormat, err)
			}
		}
	}
}

func TestChecksumManifestValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "checksums.txt")
	valid := strings.Repeat("a", 64) + "  convai_Linux_arm64.tar.gz\n"
	for _, value := range []string{valid, valid + valid, "wrong  archive\n", strings.Replace(valid, "convai_Linux", "../convai_Linux", 1)} {
		if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := readChecksums(file)
		if (err == nil) != (value == valid) {
			t.Fatalf("manifest result = %v", err)
		}
	}
}

func TestInstallationKeepsBinaryAndXDGConfigSeparate(t *testing.T) {
	prefix := t.TempDir()
	installed, err := installBinary(prefix, []byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(installed) != filepath.Join(prefix, "bin") {
		t.Fatal("binary is not in the installation's bin directory")
	}
	data, err := os.ReadFile(installed)
	if err != nil || string(data) != "binary" {
		t.Fatal("installation did not preserve binary contents")
	}
	config := filepath.Join(prefix, "convai", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal("binary collided with the XDG configuration directory:", err)
	}
	if err := os.WriteFile(config, []byte("profiles: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
