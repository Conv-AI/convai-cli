package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
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

func validateAuthority(parsed *url.URL) (string, error) {
	host, port := parsed.Hostname(), parsed.Port()
	if host == "" {
		return "", errors.New("API URL must contain a host")
	}
	canonicalPort := port
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("API URL port must be numeric and in 1..65535")
		}
		canonicalPort = strconv.Itoa(value)
	}
	authority := host
	if strings.HasPrefix(parsed.Host, "[") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return "", errors.New("API URL must use a valid bracketed IPv6 address")
		}
		authority = "[" + host + "]"
	}
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}
	// Check the parsed authority before dropping default ports or changing case.
	// net/url can accept empty ports and unbracketed IPv6 authorities.
	if parsed.Host != authority {
		return "", errors.New("API URL authority must not contain an empty port or unbracketed IPv6 address")
	}
	return canonicalPort, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
