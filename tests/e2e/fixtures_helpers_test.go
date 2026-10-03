package e2e

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	if os.Getenv("CONVAI_E2E_REQUIRED") == "1" {
		t.Fatalf("required staging fixture is absent: %s", name)
	}
	t.Skipf("set %s for opt-in staging verification", name)
	return ""
}

func requireOptIn(t *testing.T, name string) {
	t.Helper()
	if fixture(t, name) != "1" {
		t.Fatalf("%s must be exactly 1 to authorize staging verification", name)
	}
}

func commandEnvironment(parent []string, h harness) []string {
	env := make([]string, 0, len(parent)+3)
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || name == "XDG_CONFIG_HOME" || name == "CONVAI_TOKEN" || strings.HasPrefix(name, "CONVAI_E2E_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+h.home, "XDG_CONFIG_HOME="+h.home, "CONVAI_TOKEN="+h.token)
}
