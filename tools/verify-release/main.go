// verify-release validates local GoReleaser archives and installs only the
// current host's executable into a temporary directory for offline smoke tests.
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var archiveNames = []string{
	"convai_Darwin_x86_64.tar.gz", "convai_Darwin_arm64.tar.gz",
	"convai_Linux_x86_64.tar.gz", "convai_Linux_arm64.tar.gz", "convai_Windows_x86_64.zip",
}

const maxBinaryBytes = 100 << 20

var legalFiles = []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.txt"}

func main() {
	dir := flag.String("dir", "dist", "local GoReleaser output directory")
	flag.Parse()
	if err := verify(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Verified five archive checksums/contents and native installed executable (version, help, profile import, config, missing credential).")
}

func verify(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return err
	}
	var metadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Version == "" {
		return errors.New("release metadata has no version")
	}
	checksums, err := readChecksums(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return err
	}
	native := "convai_" + map[string]string{"darwin": "Darwin", "linux": "Linux", "windows": "Windows"}[runtime.GOOS] + "_" + map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if runtime.GOOS == "windows" {
		native += ".zip"
	} else {
		native += ".tar.gz"
	}
	var installed []byte
	for _, name := range archiveNames {
		archive, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(archive)
		if checksums[name] != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("missing or incorrect checksum for %s", name)
		}
		binary, err := readArchive(name, archive)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if name == native {
			installed = binary
		}
	}
	if installed == nil {
		return fmt.Errorf("no supported native archive for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	prefix, err := os.MkdirTemp("", "convai-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(prefix)
	executable, err := installBinary(prefix, installed)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	env := []string{"HOME=" + prefix, "USERPROFILE=" + prefix, "APPDATA=" + prefix, "XDG_CONFIG_HOME=" + prefix, "CONVAI_TOKEN="}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "TMP", "TEMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	run := func(args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, executable, args...)
		command.Env = env
		return command.CombinedOutput()
	}
	version, err := run("version")
	if err != nil || strings.TrimSpace(string(version)) != metadata.Version {
		return fmt.Errorf("installed version mismatch: %s, %v", version, err)
	}
	for _, args := range [][]string{{"--help"}, {"auth", "login", "--help"}, {"config", "get", "--output", "json"}} {
		if output, err := run(args...); err != nil {
			return fmt.Errorf("installed smoke %v: %s, %w", args, output, err)
		}
	}
	profile := filepath.Join(prefix, "approved.yaml")
	profileName := "archive-check-" + filepath.Base(prefix)
	if err := os.WriteFile(profile, []byte("api_url: https://api.customer.invalid\ncredential_type: api_key\n"), 0o600); err != nil {
		return err
	}
	if output, err := run("config", "import", "--file", profile, "--profile", profileName); err != nil {
		return fmt.Errorf("installed profile import: %s, %w", output, err)
	}
	output, err := run("config", "get", "--profile", profileName, "--output", "json")
	if err != nil || !bytes.Contains(output, []byte("https://api.customer.invalid")) {
		return fmt.Errorf("installed profile readback: %s, %v", output, err)
	}
	// A status read may report an unavailable native keychain in headless CI,
	// but it must never report a credential or contact a resource server.
	if output, err := run("auth", "status", "--profile", profileName); err == nil {
		return fmt.Errorf("fresh installation unexpectedly has credentials: %s", output)
	}
	return nil
}

func installBinary(prefix string, binary []byte) (string, error) {
	dir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "convai"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(dir, name)
	return executable, os.WriteFile(executable, binary, 0o700)
}

func readChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 || len(parts[0]) != 64 || values[parts[1]] != "" || filepath.Base(parts[1]) != parts[1] {
			return nil, errors.New("invalid checksum manifest")
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return nil, errors.New("invalid SHA256 checksum")
		}
		values[parts[1]] = parts[0]
	}
	return values, scanner.Err()
}

func readArchive(name string, data []byte) ([]byte, error) {
	binaryName := "convai"
	if strings.HasSuffix(name, ".zip") {
		binaryName += ".exe"
	}
	seen := map[string]bool{}
	var binary []byte
	member := func(name string, regular bool, mode os.FileMode, reader io.Reader) error {
		if seen[name] || !regular || (name != binaryName && name != "README.md" && name != "LICENSE" && name != "NOTICE" && name != "THIRD_PARTY_NOTICES.txt" && name != "CHANGELOG.md") {
			return fmt.Errorf("unexpected archive member %q", name)
		}
		seen[name] = true
		if name != binaryName {
			if name == "LICENSE" || name == "NOTICE" || name == "THIRD_PARTY_NOTICES.txt" {
				data, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
				if err != nil || len(bytes.TrimSpace(data)) == 0 || len(data) > 4<<20 {
					return fmt.Errorf("invalid or empty archive legal file %s", name)
				}
			}
			return nil
		}
		if binaryName == "convai" && mode&0o111 == 0 {
			return errors.New("archive binary is not executable")
		}
		var err error
		binary, err = io.ReadAll(io.LimitReader(reader, maxBinaryBytes+1))
		if err != nil || len(binary) == 0 || len(binary) > maxBinaryBytes {
			return errors.New("invalid archive binary size")
		}
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			stream, err := file.Open()
			if err != nil {
				return nil, err
			}
			err = member(file.Name, file.Mode().IsRegular(), file.Mode(), stream)
			stream.Close()
			if err != nil {
				return nil, err
			}
		}
	} else {
		compressed, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer compressed.Close()
		reader := tar.NewReader(compressed)
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if err := member(header.Name, header.Typeflag == tar.TypeReg, os.FileMode(header.Mode), reader); err != nil {
				return nil, err
			}
		}
	}
	if binary == nil {
		return nil, errors.New("archive has no executable")
	}
	for _, name := range legalFiles {
		if !seen[name] {
			return nil, fmt.Errorf("archive has no %s", name)
		}
	}
	return binary, nil
}
