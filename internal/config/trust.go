package config

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

func ValidateTrust(profile Profile) error {
	apiURL, err := NormalizeAPIURL(profile.APIURL)
	if err != nil {
		return err
	}
	if profile.CABundle != "" {
		if !filepath.IsAbs(profile.CABundle) {
			return errors.New("ca_bundle must be an absolute path to a PEM CA bundle")
		}
		if !strings.HasPrefix(apiURL, "https://") {
			return errors.New("customer CA profiles require HTTPS")
		}
	}
	trust := profile.OAuth
	if trust == (OAuthTrust{}) {
		return nil
	}
	if apiURL == DefaultAPIURL || apiURL == StagingAPIURL || apiURL == PreviewAPIURL {
		return errors.New("custom OAuth mappings cannot replace the fixed Convai-hosted mappings")
	}
	if !strings.HasPrefix(apiURL, "https://") || trust.APIURL != apiURL {
		return errors.New("OAuth api_url must exactly match the profile's canonical HTTPS origin")
	}
	issuer, err := NormalizeAPIURL(trust.Issuer)
	if err != nil || !strings.HasPrefix(issuer, "https://") || issuer != trust.Issuer {
		return errors.New("OAuth issuer must be a canonical HTTPS origin")
	}
	resource, err := url.ParseRequestURI(trust.Resource)
	if err != nil || resource.Scheme != "https" || resource.Host == "" || resource.User != nil ||
		resource.RawQuery != "" || resource.ForceQuery || resource.Fragment != "" ||
		resource.RawPath != "" || strings.Contains(resource.Path, "..") {
		return errors.New("OAuth resource must be an absolute HTTPS URI without credentials, query, fragment, or encoded path")
	}
	origin, err := NormalizeAPIURL(resource.Scheme + "://" + resource.Host)
	if err != nil || origin != resource.Scheme+"://"+resource.Host {
		return errors.New("OAuth resource must use a canonical HTTPS origin")
	}
	return nil
}
