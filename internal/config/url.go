package config

import (
	"errors"
	"net/url"
)

func parseURL(raw string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, &url.Error{Op: "parse", URL: raw, Err: err}
	}
	if parsed.Host == "" {
		return nil, &url.Error{Op: "parse", URL: raw, Err: errors.New("host is required")}
	}
	return parsed, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
