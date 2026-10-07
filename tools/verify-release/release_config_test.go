package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleasePublishesVerifierMetadataAndChecksOwnerBeforePublication(t *testing.T) {
	data, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Release struct {
			ExtraFiles []struct {
				Glob string `yaml:"glob"`
			} `yaml:"extra_files"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
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
	contractGate, publication := -1, -1
	metadataAttested := false
	for index, step := range workflow.Jobs["release"].Steps {
		if step.Run == "make contract-check" {
			contractGate = index
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
	if !metadataAttested {
		t.Fatal("published verifier metadata must have provenance")
	}
}
