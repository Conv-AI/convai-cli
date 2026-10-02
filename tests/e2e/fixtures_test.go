package e2e

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestCommandEnvironmentIsolatesFixtureCredentials(t *testing.T) {
	got := commandEnvironment([]string{
		"PATH=/bin", "HOME=old", "XDG_CONFIG_HOME=old", "CONVAI_TOKEN=old",
		"CONVAI_E2E_FIREBASE_ID_TOKEN=management", "CONVAI_E2E_OTHER_TOKEN=other",
	}, harness{home: "isolated", token: "selected"})
	want := []string{"PATH=/bin", "HOME=isolated", "XDG_CONFIG_HOME=isolated", "CONVAI_TOKEN=selected"}
	if !slices.Equal(got, want) {
		t.Fatal("CLI child inherited unrelated fixture secrets or duplicate overrides")
	}
}

func TestFixturePolicyChild(t *testing.T) {
	if os.Getenv("CLI_FIXTURE_POLICY_CHILD") != "1" {
		return
	}
	requireOptIn(t, "CONVAI_E2E_MUTATIONS")
}

func TestStrictFixturePolicyCannotSkip(t *testing.T) {
	for _, test := range []struct {
		name, required, optIn string
		fail, skip            bool
	}{
		{"optional-missing", "", "", false, true},
		{"required-missing", "1", "", true, false},
		{"incorrect-opt-in", "1", "true", true, false},
		{"required-present", "1", "1", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestFixturePolicyChild$", "-test.v")
			command.Env = append(commandEnvironment(os.Environ(), harness{home: t.TempDir(), token: "offline"}),
				"CLI_FIXTURE_POLICY_CHILD=1", "CONVAI_E2E_REQUIRED="+test.required, "CONVAI_E2E_MUTATIONS="+test.optIn)
			output, err := command.CombinedOutput()
			if (err != nil) != test.fail || strings.Contains(string(output), "--- SKIP:") != test.skip {
				t.Fatal("fixture policy falsely passed or skipped a required gate")
			}
		})
	}
}
