package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Transport authentication and the browser defences (docs/MCP.md §2).
//
// This is a real HTTP listener on the loopback interface, which means it is
// reachable by every process on the machine and — through a browser — by every
// web page the user visits. The token handles the first. The Origin and Host
// checks handle the second, and they are the part that is easy to leave out
// because nothing appears broken when they are missing.

// TokenBytes is the token's length in bytes before encoding. 32 bytes of
// crypto/rand is 256 bits; base64url makes it 43 characters, always, which is
// why comparing lengths leaks nothing.
const TokenBytes = 32

// MintToken returns a new bearer token.
func MintToken() (string, error) {
	buf := make([]byte, TokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcp: minting a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// An Auth holds the current token and answers whether a presented one matches.
//
// The token is never persisted beyond the endpoint file the user's client
// reads (§2), is minted when the server is enabled, and is revoked — not
// rotated — by the kill switch (§10): there is no way to get the old one back,
// because a kill switch that leaves the credential valid is a pause button.
type Auth struct {
	mu sync.RWMutex
	// token is "" when no server is running or the switch has been thrown.
	token string
}

// Mint generates a token, installs it, and returns it. Any previous token
// stops working.
func (a *Auth) Mint() (string, error) {
	token, err := MintToken()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = token
	return token, nil
}

// Revoke invalidates the current token. Every subsequent request fails
// authentication, including one presenting no token at all.
func (a *Auth) Revoke() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = ""
}

// Active reports whether a token is installed.
func (a *Auth) Active() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token != ""
}

// Valid reports whether presented is the current token.
//
// It is false whenever there is no token, so a revoked server cannot be
// reached by presenting the empty string — the case a plain string comparison
// gets wrong.
func (a *Auth) Valid(presented string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(a.token)) == 1
}

// bearer extracts the token from an Authorization header value.
func bearer(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// Guard wraps next with the three checks every request must pass.
//
// The order is Origin, then Host, then the token: the two browser checks are
// about *who is calling* and can be answered without touching the credential,
// so a page probing the server learns nothing about the token from how long a
// rejection took.
func (a *Auth) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The DNS-rebinding defence, and it is not optional: without it any
		// page the user visits can drive this server through their browser.
		// A rebinding attack points its own hostname at 127.0.0.1, and the
		// browser then sends that hostname — not ours — in both of these.
		if origin := r.Header.Get("Origin"); origin != "" && !originIsLoopback(origin) {
			refuse(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if !hostIsLoopback(r.Host) {
			refuse(w, http.StatusForbidden, "host not allowed")
			return
		}
		if !a.Valid(bearer(r.Header.Get("Authorization"))) {
			refuse(w, http.StatusUnauthorized, "invalid or missing token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// refuse writes a rejection.
//
// The reason is a fixed string chosen from this file, never anything derived
// from the request, and it never restates what was presented: an error that
// echoed the Authorization header back would put the token in whatever log the
// caller keeps.
func refuse(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, reason)
}

// originIsLoopback reports whether an Origin header value is a loopback
// origin. A "null" origin — a sandboxed iframe, a file:// page — is not.
func originIsLoopback(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return hostIsLoopback(u.Host)
}

// hostIsLoopback reports whether an authority is loopback.
//
// "localhost" counts here, which looks like a contradiction of §2's rule that
// the listener must bind 127.0.0.1 and never "localhost". It is not: that rule
// is about *binding*, where a name resolving to something other than loopback
// would expose the listener to the network. This is about a name a browser
// reports for a page it is already running, and no rebinding attack can make
// the browser claim "localhost" for a page served from somewhere else.
func hostIsLoopback(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
