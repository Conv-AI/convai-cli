package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectPreservesNoticesAndRejectsConflicts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "NOTICE")
	if err := os.WriteFile(file, []byte("Upstream attribution\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notices := map[string]string{}
	if err := collect(dir, "component/", notices, false); err != nil {
		t.Fatal(err)
	}
	if notices["component/NOTICE"] != "Upstream attribution\n" {
		t.Fatal("upstream notice was altered")
	}
	if err := collect(dir, "component/", notices, false); err != nil {
		t.Fatal("identical platform notices must deduplicate:", err)
	}
	if err := os.WriteFile(file, []byte("Different platform notice"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collect(dir, "component/", notices, false); err == nil {
		t.Fatal("conflicting platform notices must not overwrite each other")
	}
	if err := os.WriteFile(file, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collect(dir, "", map[string]string{}, false); err == nil {
		t.Fatal("empty notices must fail")
	}
}

func TestCollectGoVendorOnlyLegalFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"LICENSE", "PATENTS", "NOTICE", "implementation.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	notices := map[string]string{}
	if err := collect(dir, "Go/", notices, true); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 3 || notices["Go/implementation.go"] != "" {
		t.Fatal("only legal files should be collected from Go vendor source")
	}
}
