package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const stagingIssuer = "https://login-stg.convai.com"
const stagingResource = "https://mcp-api-stg.convai.com/mcp"

type issuedCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ID           string
}

type managementClient struct{ http *http.Client }

func newManagementClient() managementClient {
	return managementClient{http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c managementClient) request(method, path, contentType, bearer string, body []byte, expected int, dst any) error {
	req, err := http.NewRequest(method, stagingIssuer+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot construct staging management request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return errors.New("staging management request failed")
	}
	defer response.Body.Close()
	var statusError error
	if response.StatusCode != expected {
		statusError = fmt.Errorf("staging management HTTP %d; expected %d", response.StatusCode, expected)
	}
	if dst == nil {
		return statusError
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.Join(statusError, errors.New("invalid staging management response size"))
	}
	if json.Unmarshal(data, dst) != nil {
		return errors.Join(statusError, errors.New("invalid staging management JSON response"))
	}
	return statusError
}

func (c managementClient) createPAT(firebase string, scopes []string) (issuedCredential, error) {
	body, err := json.Marshal(map[string]any{
		"name":   "CLI staging E2E " + time.Now().UTC().Format(time.RFC3339Nano),
		"scopes": scopes, "expires_in_days": 1,
	})
	if err != nil {
		return issuedCredential{}, errors.New("cannot encode PAT request")
	}
	var result struct {
		Token string `json:"token"`
		Item  struct {
			ID string `json:"id"`
		} `json:"personal_access_token"`
	}
	if err := c.request("POST", "/auth/personal-access-tokens", "application/json", firebase, body, 201, &result); err != nil {
		if result.Item.ID != "" {
			if cleanup := c.revokePAT(firebase, issuedCredential{ID: result.Item.ID}); cleanup != nil {
				return issuedCredential{}, errors.Join(err, fmt.Errorf("PAT cleanup failed: %w", cleanup))
			}
		}
		return issuedCredential{}, err
	}
	if !strings.HasPrefix(result.Token, "cvpat_") || result.Item.ID == "" {
		if result.Item.ID != "" {
			if err := c.revokePAT(firebase, issuedCredential{ID: result.Item.ID}); err != nil {
				return issuedCredential{}, fmt.Errorf("invalid PAT response; cleanup failed: %w", err)
			}
		}
		return issuedCredential{}, errors.New("PAT response omitted credential or revocation ID; verify issuance before rerunning")
	}
	return issuedCredential{AccessToken: result.Token, ID: result.Item.ID}, nil
}

func (c managementClient) exchangeOAuth(path string, form url.Values) (issuedCredential, error) {
	var credential issuedCredential
	err := c.request("POST", path, "application/x-www-form-urlencoded", "", []byte(form.Encode()), 200, &credential)
	if err == nil && !strings.HasPrefix(credential.AccessToken, "cvoat_") {
		err = errors.New("unexpected OAuth access-token type")
	}
	if err == nil && credential.RefreshToken == "" {
		err = errors.New("OAuth fixture omitted its refresh token")
	}
	if err != nil {
		if cleanup := c.revokeOAuth(credential); cleanup != nil {
			err = errors.Join(err, fmt.Errorf("OAuth cleanup failed: %w", cleanup))
		}
		return issuedCredential{}, err
	}
	return credential, nil
}

func (c managementClient) revokePAT(firebase string, credential issuedCredential) error {
	return c.request("DELETE", "/auth/personal-access-tokens/"+url.PathEscape(credential.ID), "", firebase, nil, 200, nil)
}

func (c managementClient) revokeOAuth(credential issuedCredential) error {
	var failures []error
	for _, token := range []string{credential.AccessToken, credential.RefreshToken} {
		if token == "" {
			continue
		}
		form := url.Values{"token": {token}, "client_id": {"codex"}}
		if err := c.request("POST", "/auth/oauth/revoke", "application/x-www-form-urlencoded", "", []byte(form.Encode()), 200, nil); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (c managementClient) verifyOAuthRefreshRevoked(credential issuedCredential) error {
	if credential.RefreshToken == "" {
		return errors.New("cannot verify revocation without an issued refresh token")
	}
	form := oauthRefreshForm(credential)
	var result struct {
		issuedCredential
		Error string `json:"error"`
	}
	err := c.request("POST", "/auth/oauth/token", "application/x-www-form-urlencoded", "", []byte(form.Encode()), 400, &result)
	if err == nil && result.Error == "invalid_grant" && result.AccessToken == "" && result.RefreshToken == "" {
		return nil
	}
	err = errors.Join(errors.New("revoked OAuth refresh grant was not rejected with HTTP 400 invalid_grant"), err)
	// A broken revocation boundary can issue a fresh pair; revoke it before failing.
	if cleanup := c.revokeOAuth(result.issuedCredential); cleanup != nil {
		err = errors.Join(err, fmt.Errorf("unexpected refresh credentials cleanup failed: %w", cleanup))
	}
	return err
}

func (c managementClient) refreshOAuth(credential issuedCredential) (issuedCredential, error) {
	return c.exchangeOAuth("/auth/oauth/token", oauthRefreshForm(credential))
}

func oauthRefreshForm(credential issuedCredential) url.Values {
	return url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"codex"},
		"refresh_token": {credential.RefreshToken}, "resource": {stagingResource},
	}
}
