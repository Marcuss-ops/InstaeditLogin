package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// peerHost returns the IP portion of a "host:port" address. If the
// address cannot be split, it is returned as-is so callers can attempt
// to parse it as a bare IP.
func peerHost(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// ParseTrustedProxies parses a comma-separated list of IP addresses
// and/or CIDR ranges (e.g. "10.0.0.0/8,127.0.0.1"). It returns the
// parsed networks so callers can decide whether an X-Forwarded-For or
// X-Real-IP header should be trusted. Empty input returns an empty
// slice and no error.
func ParseTrustedProxies(s string) ([]*net.IPNet, error) {
	if s == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Try CIDR first, then single IP.
		if _, ipnet, err := net.ParseCIDR(part); err == nil {
			out = append(out, ipnet)
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, fmt.Errorf("trusted proxy %q is not a valid IP or CIDR", part)
		}
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, &net.IPNet{IP: ip4, Mask: net.CIDRMask(32, 32)})
		} else {
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
	}
	return out, nil
}

// trustedClientIP returns the original client IP for the request. If
// the immediate peer (RemoteAddr) is in the trusted proxy list, the
// X-Forwarded-For or X-Real-IP headers are considered; otherwise the
// function returns the peer address. This prevents clients from
// spoofing their IP when the API is reachable directly.
func trustedClientIP(r *http.Request, trusted []*net.IPNet) string {
	peer := peerHost(r.RemoteAddr)
	if !isTrustedProxy(peer, trusted) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The leftmost value is the original client.
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return peer
}

// isTrustedProxy reports whether the given peer address matches any
// of the configured trusted proxy networks.
func isTrustedProxy(peer string, trusted []*net.IPNet) bool {
	if peer == "" || len(trusted) == 0 {
		return false
	}
	ip := net.ParseIP(peer)
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
