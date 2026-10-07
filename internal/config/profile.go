package config

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeProfile rejects unknown fields (including secrets), duplicate keys,
// and trailing documents without echoing user-supplied values in diagnostics.
func DecodeProfile(data []byte) (Profile, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, errors.New("profile must be one YAML mapping with only supported non-secret fields")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Profile{}, errors.New("profile must contain exactly one YAML document")
	}
	if profile.CredentialType == "" {
		profile.CredentialType = CredentialAPIKey
	}
	var err error
	profile.APIURL, err = NormalizeAPIURL(profile.APIURL)
	if err != nil {
		return Profile{}, errors.New("profile must contain a valid API origin")
	}
	cfg := Config{CurrentProfile: DefaultProfileName, Profiles: map[string]Profile{DefaultProfileName: profile}}
	if err := Validate(cfg); err != nil {
		return Profile{}, err
	}
	return profile, nil
}
