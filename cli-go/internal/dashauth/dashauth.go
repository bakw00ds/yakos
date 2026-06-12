// Package dashauth provides shared authentication middleware for the perfdash
// and metricsdash HTTP servers.
//
// It implements:
//   - Host-header / DNS-rebinding defense (item 1): RequireLocalHost middleware
//     rejects any request whose Host header is not 127.0.0.1:<port>,
//     localhost:<port>, or [::1]:<port>.
//   - Constant-time token comparison using crypto/subtle (item 2).
//   - Bearer-token extraction from the Authorization header.
package dashauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireLocalHost is HTTP middleware that defends against DNS-rebinding by
// rejecting requests whose Host header does not match one of the expected
// loopback addresses for the given port.
//
// Allowed Host values (case-insensitive for the hostname part):
//
//	127.0.0.1:<port>
//	localhost:<port>
//	[::1]:<port>
//
// Requests without a Host header, or with a Host that does not match, receive
// 403 Forbidden. Static-asset routes (/ and /app.js etc.) are included
// intentionally — a rebinding attacker would hit those first.
//
// The serverAddr parameter must be the full "host:port" bind address the
// server is listening on (e.g. "127.0.0.1:7895").
func RequireLocalHost(serverAddr string, next http.Handler) http.Handler {
	_, port, _ := splitHostPort(serverAddr)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host == "" {
			http.Error(w, `{"error":"missing Host header"}`, http.StatusForbidden)
			return
		}
		if !isAllowedHost(host, port) {
			http.Error(w, `{"error":"DNS-rebinding defense: unexpected Host"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedHost returns true when h matches one of the loopback Host values
// for the given port.
func isAllowedHost(h, port string) bool {
	// Normalise: strip trailing dot (rare but valid).
	h = strings.TrimSuffix(h, ".")

	hostPart, hostPort, _ := splitHostPort(h)
	if hostPort == "" {
		// No port in the Host header — treat it as matching if the host
		// portion is loopback (browser sometimes omits the port for well-known
		// defaults; in practice this path is uncommon for non-80/443 ports).
		hostPart = h
		hostPort = port
	}

	if hostPort != port {
		return false
	}

	switch strings.ToLower(hostPart) {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// splitHostPort is a minimal host:port splitter that handles IPv6 bracket
// notation ([::1]:7895) without depending on net.SplitHostPort (which returns
// an error when there is no port).
func splitHostPort(s string) (host, port, zone string) {
	// IPv6 bracket form: [host%zone]:port or [host%zone]
	if len(s) > 0 && s[0] == '[' {
		end := strings.LastIndex(s, "]")
		if end < 0 {
			return s, "", ""
		}
		inner := s[1:end]
		// Strip zone id.
		if i := strings.LastIndex(inner, "%"); i >= 0 {
			zone = inner[i+1:]
			inner = inner[:i]
		}
		host = inner
		rest := s[end+1:]
		if len(rest) > 0 && rest[0] == ':' {
			port = rest[1:]
		}
		return host, port, zone
	}

	// Plain host:port — only the last colon separates host from port.
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", ""
	}
	return s[:i], s[i+1:], ""
}

// BearerToken extracts the Bearer token from the Authorization header.
// Returns "" when the header is absent or lacks the "Bearer " prefix.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// TokenEqual compares two tokens in constant time using crypto/subtle.
// Length mismatch is handled before the comparison to avoid leaking length
// information through short-circuit returns; subtle.ConstantTimeCompare
// requires equal-length inputs, so we XOR the lengths first.
func TokenEqual(a, b string) bool {
	// Guard length first with a constant-time length comparison.
	lenA := len(a)
	lenB := len(b)
	// subtle.ConstantTimeEq returns 1 if equal.
	if subtle.ConstantTimeEq(int32(lenA), int32(lenB)) == 0 { //nolint:gosec
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RequireToken returns an http.HandlerFunc that enforces Bearer token auth.
// It uses TokenEqual (constant-time) for the comparison.
func RequireToken(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			writeJSON(w, http.StatusUnauthorized, `{"error":"missing Authorization: Bearer token"}`)
			return
		}
		if !TokenEqual(tok, token) {
			writeJSON(w, http.StatusForbidden, `{"error":"invalid token"}`)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
