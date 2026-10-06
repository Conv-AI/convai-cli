package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveValidation(t *testing.T) {
	for _, zipFormat := range []bool{false, true} {
		for _, names := range [][]string{{"convai"}, {"convai", "README.md"}, {"../convai"}, {"config.yaml"}, {"convai", "convai"}, {"convai", ".env"}} {
			var buffer bytes.Buffer
			fileName := "test.tar.gz"
			if zipFormat {
				fileName = "test.zip"
				writer := zip.NewWriter(&buffer)
				for _, name := range names {
					if name == "convai" {
						name += ".exe"
					}
					stream, err := writer.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = stream.Write([]byte("binary"))
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				compressed := gzip.NewWriter(&buffer)
				writer := tar.NewWriter(compressed)
				for _, name := range names {
					if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 6, Typeflag: tar.TypeReg}); err != nil {
						t.Fatal(err)
					}
					_, _ = writer.Write([]byte("binary"))
				}
				_ = writer.Close()
				_ = compressed.Close()
			}
			wantValid := len(names) == 1 && names[0] == "convai" || len(names) == 2 && names[1] == "README.md"
			_, err := readArchive(fileName, buffer.Bytes())
			if (err == nil) != wantValid {
				t.Fatalf("archive %v, zip %v: %v", names, zipFormat, err)
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
