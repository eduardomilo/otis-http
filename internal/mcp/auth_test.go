package mcp

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reached(a *Auth) (http.Handler, *bool) {
	got := false
	return a.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = true
		w.WriteHeader(http.StatusOK)
	})), &got
}

// A request that should get through, which each test then breaks one part of.
func goodRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:51234"+MCPPath, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestTheTokenIs256BitsAndNeverRepeats(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		token, err := MintToken()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("the token is not base64url: %v", err)
		}
		if len(raw) != TokenBytes {
			t.Fatalf("the token is %d bytes, want %d", len(raw), TokenBytes)
		}
		// Fixed length, which is why comparing lengths leaks nothing.
		if len(token) != 43 {
			t.Fatalf("the encoded token is %d characters, want 43", len(token))
		}
		if seen[token] {
			t.Fatal("a token repeated")
		}
		seen[token] = true
	}
}

// The case a plain string comparison gets wrong: with no token installed, an
// empty token must not match an empty field.
func TestNoTokenMeansNothingIsValid(t *testing.T) {
	var a Auth
	for _, presented := range []string{"", " ", "null", "undefined"} {
		if a.Valid(presented) {
			t.Errorf("Valid(%q) on a server with no token", presented)
		}
	}
	if a.Active() {
		t.Error("Active() with no token")
	}
}

// The kill switch revokes rather than rotates: a switch that left the old
// credential working is a pause button (§10).
func TestRevokeClosesTheDoorOnTheOldToken(t *testing.T) {
	var a Auth
	token, err := a.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if !a.Valid(token) {
		t.Fatal("the minted token is not valid")
	}

	handler, got := reached(&a)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, goodRequest(token))
	if !*got || w.Code != http.StatusOK {
		t.Fatalf("a good request was refused: %d", w.Code)
	}

	a.Revoke()
	if a.Valid(token) || a.Active() {
		t.Error("the token survived revocation")
	}
	*got = false
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, goodRequest(token))
	if *got {
		t.Error("the handler ran after revocation")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	// A new token works and the old one still does not.
	fresh, err := a.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if a.Valid(token) {
		t.Error("the revoked token came back with the new one")
	}
	if !a.Valid(fresh) {
		t.Error("the fresh token does not work")
	}
}

// The DNS-rebinding defence. An attacking page points its own hostname at
// 127.0.0.1; the browser then sends *that* hostname, so this is the check that
// tells the two apart — and the reason a valid token is not sufficient.
func TestOriginAndHostAreTheRebindingDefence(t *testing.T) {
	var a Auth
	token, err := a.Mint()
	if err != nil {
		t.Fatal(err)
	}
	handler, got := reached(&a)

	for _, tc := range []struct {
		name          string
		origin, host  string
		wantReached   bool
		wantStatusNot int
	}{
		{name: "no origin, loopback host", host: "127.0.0.1:51234", wantReached: true},
		{name: "loopback origin", origin: "http://127.0.0.1:51234", host: "127.0.0.1:51234", wantReached: true},
		{name: "localhost origin", origin: "http://localhost:3000", host: "127.0.0.1:51234", wantReached: true},
		{name: "ipv6 loopback origin", origin: "http://[::1]:3000", host: "[::1]:51234", wantReached: true},
		{name: "127.x.x.x origin", origin: "http://127.0.0.2:3000", host: "127.0.0.1:51234", wantReached: true},

		{name: "a web page", origin: "https://evil.example", host: "127.0.0.1:51234"},
		{name: "http web page", origin: "http://evil.example", host: "127.0.0.1:51234"},
		{name: "a null origin", origin: "null", host: "127.0.0.1:51234"},
		{name: "a hostname that looks loopback", origin: "http://127.0.0.1.evil.example", host: "127.0.0.1:51234"},
		{name: "a hostname prefixed loopback", origin: "http://localhost.evil.example", host: "127.0.0.1:51234"},
		{name: "rebinding: the attacker's host", host: "evil.example"},
		{name: "rebinding with a port", host: "evil.example:51234"},
		{name: "a bare non-loopback ip host", host: "10.0.0.5:51234"},
		{name: "an empty host", host: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*got = false
			r := goodRequest(token)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if *got != tc.wantReached {
				t.Fatalf("reached = %v, want %v (status %d)", *got, tc.wantReached, w.Code)
			}
			if !tc.wantReached && w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestTheBearerTokenIsRequired(t *testing.T) {
	var a Auth
	token, err := a.Mint()
	if err != nil {
		t.Fatal(err)
	}
	other, err := MintToken()
	if err != nil {
		t.Fatal(err)
	}
	handler, got := reached(&a)

	for _, tc := range []struct {
		name, header string
		wantReached  bool
	}{
		{name: "the token", header: "Bearer " + token, wantReached: true},
		// The scheme is case-insensitive per RFC 7235, and a client that
		// sends "bearer" is not an attacker.
		{name: "lowercase scheme", header: "bearer " + token, wantReached: true},
		{name: "no header"},
		{name: "no scheme", header: token},
		{name: "empty bearer", header: "Bearer "},
		{name: "another token", header: "Bearer " + other},
		{name: "a prefix of the token", header: "Bearer " + token[:20]},
		{name: "the token plus a byte", header: "Bearer " + token + "x"},
		{name: "basic auth", header: "Basic " + token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*got = false
			r := goodRequest(token)
			r.Header.Del("Authorization")
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if *got != tc.wantReached {
				t.Fatalf("reached = %v, want %v", *got, tc.wantReached)
			}
			if !tc.wantReached && w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

// A rejection must not restate what was presented: an error echoing the
// Authorization header back puts the token in whatever log the caller keeps.
func TestARejectionNeverEchoesTheToken(t *testing.T) {
	var a Auth
	token, err := a.Mint()
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := reached(&a)

	for _, tc := range []struct{ name, header, host string }{
		{"wrong host", "Bearer " + token, "evil.example"},
		{"wrong token", "Bearer " + token + "x", "127.0.0.1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := goodRequest(token)
			r.Header.Set("Authorization", tc.header)
			r.Host = tc.host
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			body := w.Body.String()
			if strings.Contains(body, token) {
				t.Errorf("the rejection echoes the token: %s", body)
			}
			if strings.Contains(body, tc.host) && tc.host != "127.0.0.1:1" {
				t.Errorf("the rejection echoes the request's host: %s", body)
			}
		})
	}
}

// The order of the checks: the two browser checks answer "who is calling" and
// must not require touching the credential, so a page probing the server
// cannot learn from a rejection whether its token was even looked at.
func TestTheBrowserChecksComeBeforeTheToken(t *testing.T) {
	var a Auth
	if _, err := a.Mint(); err != nil {
		t.Fatal(err)
	}
	handler, _ := reached(&a)

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:51234"+MCPPath, nil)
	r.Host = "evil.example"
	// No Authorization header at all: a 403 rather than a 401 shows the host
	// was judged first.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — the host check must come first", w.Code)
	}
}
