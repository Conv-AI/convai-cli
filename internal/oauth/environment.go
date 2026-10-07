// Package oauth implements the Convai CLI's public-client browser login
// against the existing Convai Authentication service: RFC 8414 discovery,
// authorization code + PKCE (S256) over a loopback redirect, rotating refresh
// grants, and RFC 7009 revocation. Tokens are opaque and never decoded.
package oauth

import "github.com/Conv-AI/convai-cli/internal/config"

const (
	// ClientID is the dedicated predefined public client registered with Auth.
	ClientID = "convai-cli"
	// Audience is Auth's default audience for requests that omit the optional
	// resource parameter. It is a legacy identifier, not an RFC 8707 URI, so
	// the CLI never sends it; it only binds the stored session.
	Audience = "convai-api"
	// CallbackPath is the exact path of the registered loopback redirect.
	CallbackPath = "/callback"
)

// Environment maps a Convai REST API origin to the Auth issuer trusted to
// issue browser-login credentials for it.
type Environment struct {
	APIURL string
	Issuer string
}

// Environments are the immutable SaaS mappings. Custom profiles require an
// explicit operator-approved mapping; the CLI never guesses an issuer.
var Environments = []Environment{
	{APIURL: config.StagingAPIURL, Issuer: "https://login-stg.convai.com"},
	{APIURL: config.PreviewAPIURL, Issuer: "https://login-preview.convai.com"},
	{APIURL: config.DefaultAPIURL, Issuer: "https://login.convai.com"},
}

// IssuerFor returns the trusted issuer for a normalized API URL.
func IssuerFor(environments []Environment, apiURL string) (string, bool) {
	for _, environment := range environments {
		if environment.APIURL == apiURL {
			return environment.Issuer, true
		}
	}
	return "", false
}
