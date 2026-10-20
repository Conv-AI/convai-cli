package main

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseSBOMHasLeastPrivilegeArtifactDownloadPermissions(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["release"]
	permissions := workflow.Permissions
	if job.Permissions != nil {
		permissions = job.Permissions
	}
	// Explicit GitHub permissions deny omitted scopes; the SBOM action downloads
	// its Actions artifact before attaching it to the release.
	want := map[string]string{
		"actions": "read", "contents": "write",
		"id-token": "write", "attestations": "write",
	}
	if !reflect.DeepEqual(permissions, want) {
		t.Fatalf("release permissions = %v, want least-privilege scopes %v", permissions, want)
	}
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "anchore/sbom-action@") && step.With["upload-release-assets"] == true {
			return
		}
	}
	t.Fatal("release must continue publishing the SBOM; disabling it is not a permissions fix")
}

func TestReleasePublishesVerifierMetadataAndChecksOwnerBeforePublication(t *testing.T) {
	data, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Archives []struct {
			Files []string `yaml:"files"`
		} `yaml:"archives"`
		Release struct {
			ExtraFiles []struct {
				Glob string `yaml:"glob"`
			} `yaml:"extra_files"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	if len(release.Archives) == 0 {
		t.Fatal("release must configure licensed archives")
	}
	for _, archive := range release.Archives {
		for _, name := range legalFiles {
			if !slices.Contains(archive.Files, name) {
				t.Fatalf("archive configuration must include %s", name)
			}
		}
	}
	metadataPublished := false
	for _, file := range release.Release.ExtraFiles {
		metadataPublished = metadataPublished || file.Glob == "./dist/metadata.json"
	}
	if !metadataPublished {
		t.Fatal("published downloads would be missing the archive verifier's metadata")
	}
	data, err = os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
				With struct {
					SubjectPath string `yaml:"subject-path"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	contractGate, licenseGate, publication := -1, -1, -1
	metadataAttested := false
	for index, step := range workflow.Jobs["release"].Steps {
		if step.Run == "make contract-check" {
			contractGate = index
		}
		if step.Run == "make license-check" {
			licenseGate = index
		}
		if step.Name == "Publish GitHub release" {
			publication = index
		}
		for _, subject := range strings.Fields(step.With.SubjectPath) {
			metadataAttested = metadataAttested || subject == "dist/metadata.json"
		}
	}
	if contractGate < 0 || publication <= contractGate {
		t.Fatal("release must check the reviewed owner contract before publication")
	}
	if licenseGate < 0 || publication <= licenseGate {
		t.Fatal("release must check current redistribution notices before publication")
	}
	if !metadataAttested {
		t.Fatal("published verifier metadata must have provenance")
	}
}
