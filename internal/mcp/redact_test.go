package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/resolve"
)

// maskerFor returns the real masker over values, not a stand-in. What is
// under test here is the composition of this package's walk with the masking
// resolve actually performs, so substituting a simpler mask would test the
// wrong thing.
func maskerFor(values ...string) func(string) string {
	x := &resolve.Expander{Secrets: values}
	return x.Mask
}

const placeholder = resolve.MaskPlaceholder

// Verify's whole method rests on masking being idempotent: it masks the
// outbound bytes a second time and treats any change as a survivor. If Mask
// ever became non-idempotent — appending a counter, numbering the
// placeholders — verify would report a leak on every clean result and this
// package would refuse to emit anything. Assert the property here so that
// change breaks with a message naming the reason.
func TestMaskingIsIdempotentWhichIsWhatVerifyRelies0n(t *testing.T) {
	mask := maskerFor("sk-live-abc", "hunter2")
	once := mask("Bearer sk-live-abc and hunter2")
	if twice := mask(once); twice != once {
		t.Fatalf("masking is not idempotent: %q then %q — verify() depends on this", once, twice)
	}
}

func TestNestedSecretsAreMasked(t *testing.T) {
	const value = "sk-live-do-not-print"
	r := NewRedactor(maskerFor(value))

	out, err := r.Marshal(map[string]any{
		"headers": []any{
			map[string]any{"name": "Authorization", "value": "Bearer " + value},
		},
		"body": map[string]any{
			"echoed": map[string]any{"deep": []any{"x", "token=" + value}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), value) {
		t.Fatalf("the secret survived: %s", out)
	}
	if n := strings.Count(string(out), placeholder); n != 2 {
		t.Errorf("got %d masked spots, want 2: %s", n, out)
	}
}

// A tool that returns a map of header name to value is one confusion away
// from returning it the other way round, and the credential would then be
// the key.
func TestASecretInAnObjectKeyIsMasked(t *testing.T) {
	const value = "sk-live-in-the-key"
	r := NewRedactor(maskerFor(value))

	out, err := r.Marshal(map[string]string{value: "Authorization"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), value) {
		t.Fatalf("the secret survived as a key: %s", out)
	}
}

// A secret can be all digits — an account number, a PIN — and would then
// arrive as a JSON number rather than a string. decode's UseNumber is what
// keeps one masking rule covering the whole document.
func TestANumericSecretIsMasked(t *testing.T) {
	const value = "8675309"
	r := NewRedactor(maskerFor(value))

	out, err := r.Marshal(struct {
		Account int    `json:"account"`
		Note    string `json:"note"`
	}{Account: 8675309, Note: "code " + value})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), value) {
		t.Fatalf("a numeric secret survived: %s", out)
	}
	if !strings.Contains(string(out), placeholder) {
		t.Errorf("nothing was masked: %s", out)
	}
}

// The escaping trap: a secret containing a quote or a backslash appears in
// JSON in escaped form, so a scan of the marshalled bytes for the raw value
// would miss it. Redaction masks the parsed strings instead, before they are
// ever escaped.
func TestASecretContainingJSONSyntaxIsMasked(t *testing.T) {
	const value = `sk-"live"\slash`
	r := NewRedactor(maskerFor(value))

	out, err := r.Marshal(map[string]any{"header": "Bearer " + value})
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []string{value, `sk-\"live\"\\slash`} {
		if strings.Contains(string(out), form) {
			t.Fatalf("the secret survived as %q: %s", form, out)
		}
	}
}

// The fail-closed net, checked directly. These are bytes redact would never
// produce, which is precisely the case verify exists for: a walk that grew a
// blind spot when a new shape of result was added.
func TestVerifyRefusesBytesThatStillCarryASecret(t *testing.T) {
	const value = "sk-live-missed-by-the-walk"
	r := NewRedactor(maskerFor(value))

	for _, tc := range []struct{ name, json string }{
		{"a string value", `{"header":"Bearer ` + value + `"}`},
		{"an object key", `{"` + value + `":1}`},
		{"inside an array", `{"rows":["ok","` + value + `"]}`},
		{"under nesting", `{"a":{"b":[{"c":"` + value + `"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := r.verify([]byte(tc.json))
			if !errors.Is(err, ErrSecretSurvived) {
				t.Fatalf("verify accepted %s: err = %v", tc.name, err)
			}
			// The error may not carry the value it is reporting.
			if strings.Contains(err.Error(), value) {
				t.Errorf("the error message leaks the secret: %v", err)
			}
		})
	}
}

// Marshal must return the error *instead of* the bytes, so a caller that
// ignores the error emits nothing rather than the unredacted result.
//
// The non-idempotent mask below stands in for a walk that missed a spot:
// redact turns "alpha" into "beta", and verify's second pass turns that into
// "gamma", so verify sees a change and reports a survivor. There is no way to
// make the real masker do this, which is the point — this test is about the
// wiring between Marshal and verify, not about masking.
func TestMarshalWithholdsTheBytesWhenVerificationFails(t *testing.T) {
	shifting := func(s string) string {
		s = strings.ReplaceAll(s, "beta", "gamma")
		return strings.ReplaceAll(s, "alpha", "beta")
	}
	r := NewRedactor(shifting)

	out, err := r.Marshal(map[string]string{"v": "alpha"})
	if !errors.Is(err, ErrSecretSurvived) {
		t.Fatalf("err = %v, want ErrSecretSurvived", err)
	}
	if out != nil {
		t.Fatalf("bytes were returned alongside the error: %s", out)
	}
}

// A listing or a parse error has no secret in it, and must still go through
// the same call, so that no tool is written against a second path that was
// never verified.
func TestNoSecretsStillMarshalsNormally(t *testing.T) {
	out, err := NoSecrets().Marshal(map[string]any{"nodes": []string{"a", "b"}, "n": 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), `{"n":2,"nodes":["a","b"]}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Masking must not damage a result that has nothing to hide: the placeholder
// appears only where a secret was.
func TestNothingElseIsTouched(t *testing.T) {
	r := NewRedactor(maskerFor("sk-live-xyz"))
	out, err := r.Marshal(map[string]any{
		"status": 200,
		"body":   "{\n  \"id\": 7,\n  \"name\": \"Ada\"\n}",
		"ok":     true,
		"none":   nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), placeholder) {
		t.Fatalf("something was masked that was not a secret: %s", out)
	}
	for _, want := range []string{`"status":200`, `"ok":true`, `"none":null`, `\"name\": \"Ada\"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("%s is missing from %s", want, out)
		}
	}
}

// Text is for the payloads that are a whole string — an error message, an
// elicitation prompt — and masks them the same way.
func TestTextMasks(t *testing.T) {
	const value = "sk-live-in-an-error"
	r := NewRedactor(maskerFor(value))
	got := r.Text("the request to https://api/x with Bearer " + value + " failed")
	if strings.Contains(got, value) {
		t.Fatalf("Text leaked: %q", got)
	}
	if !strings.Contains(got, placeholder) {
		t.Errorf("Text masked nothing: %q", got)
	}
}
