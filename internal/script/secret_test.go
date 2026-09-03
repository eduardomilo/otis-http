package script

import (
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// The secret value every test below tries to get out. It is deliberately a
// string that would be obvious in any output.
const theSecret = "sk_live_DO_NOT_LEAK_9Xk2mP4qL7"

func secretOptions(t *testing.T, code string) Options {
	t.Helper()
	store := secrets.NewMemory()
	if err := store.Set(secrets.Key("acme-api", "staging", "apiKey"), theSecret); err != nil {
		t.Fatal(err)
	}
	env := &resolve.Environment{
		Name: "staging", Path: "env/staging.json",
		Values: map[string]resolve.EnvValue{
			"apiKey": {Secret: true, Kind: resolve.EnvSecret},
			"host":   {Value: "api.example.test", Kind: resolve.EnvString},
		},
	}
	return Options{
		Phase:      Post,
		Scripts:    []Source{{Path: "orders/_post.js", Code: code}},
		Response:   &ResponseView{Status: 200, StatusText: "OK", Body: `{"id":1}`},
		Request:    &RequestView{Method: "POST", URL: "https://x.test/orders", Path: "orders/x.http"},
		Vars:       newMapStore(),
		Secrets:    store,
		Env:        env,
		Collection: "acme-api",
	}
}

// THE TEST. Every obvious way to get text out of a script is tried, and none
// of them may produce the value.
//
// The list is the point: a handle that masks in console.log but not in a
// template literal is a handle that leaks. All three JavaScript coercion hooks
// are covered — toString, valueOf and Symbol.toPrimitive — and there is no
// fourth way for JavaScript to make text of an object.
//
// `mask` says whether that attempt should *also* show the mask. Most should:
// it proves the attempt actually ran rather than passing because nothing was
// produced. A few legitimately produce something else — coercing to a number
// gives NaN, and enumerating a handle gives nothing because every property is
// non-enumerable — and for those, producing nothing is the correct answer.
func TestSecretHandleCannotBeExfiltrated(t *testing.T) {
	attempts := []struct {
		name string
		code string
		mask bool
	}{
		{"console.log", `const h = secrets.ref("apiKey"); console.log(h);`, true},
		{"console.error", `console.error(secrets.ref("apiKey"));`, true},
		{"String()", `console.log(String(secrets.ref("apiKey")));`, true},
		{"template literal", "console.log(`${secrets.ref(\"apiKey\")}`);", true},
		{"concatenation", `console.log("" + secrets.ref("apiKey"));`, true},
		{"concatenation reversed", `console.log(secrets.ref("apiKey") + "");`, true},
		{"toString()", `console.log(secrets.ref("apiKey").toString());`, true},
		{"valueOf()", `console.log(secrets.ref("apiKey").valueOf());`, true},
		{"JSON.stringify", `console.log(JSON.stringify(secrets.ref("apiKey")));`, true},
		{"JSON.stringify nested", `console.log(JSON.stringify({t: secrets.ref("apiKey")}));`, true},
		{"Symbol.toPrimitive", `console.log(secrets.ref("apiKey")[Symbol.toPrimitive]("string"));`, true},
		{"array join", `console.log([secrets.ref("apiKey")].join(""));`, true},
		{"replace into a string", `console.log("x".replace("x", secrets.ref("apiKey")));`, true},
		{"RegExp source", `console.log(new RegExp(secrets.ref("apiKey")).source);`, true},
		{"a test name", `test(String(secrets.ref("apiKey")), () => {});`, true},
		{"a failing assertion", `test("t", () => expect(secrets.ref("apiKey")).toBe("nope"));`, true},
		{"toEqual against it", `test("t", () => expect("nope").toEqual(secrets.ref("apiKey")));`, true},
		{"toContain against it", `test("t", () => expect("nope").toContain(secrets.ref("apiKey")));`, true},
		{"a thrown error", `throw new Error(secrets.ref("apiKey"));`, true},
		{"a thrown handle", `throw secrets.ref("apiKey");`, true},
		{"prefix and suffix", `console.log(secrets.ref("apiKey").prefix("Bearer ").suffix("!"));`, true},
		{"padStart", `console.log("".padStart(4, secrets.ref("apiKey")));`, false},

		// The name is public: it is already committed in an environment file.
		{"the name property", `console.log(secrets.ref("apiKey").name);`, false},
		// Coercing to a number cannot produce the mask, and NaN is right.
		{"number coercion", `console.log(+secrets.ref("apiKey"));`, false},
		// Every property is non-enumerable, so enumeration yields nothing —
		// not even the handle's own shape.
		{"Object.keys", `console.log("keys:" + Object.keys(secrets.ref("apiKey")).join("|"));`, false},
		{"Object.values", `console.log("values:" + Object.values(secrets.ref("apiKey")).join("|"));`, false},
		{"for-in", `let o="in:"; for (const k in secrets.ref("apiKey")) o += k; console.log(o);`, false},
		{"spread", `console.log("spread:" + JSON.stringify({...secrets.ref("apiKey")}));`, false},
		{"error stack", `try { null.x; } catch (e) { console.log(e.stack); }`, false},
	}

	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			result, err := Run(secretOptions(t, attempt.code))

			// Everything the run produced, in one string: the console, the
			// tests, and the error. A leak anywhere in it is a leak.
			var everything strings.Builder
			for _, line := range result.Console {
				everything.WriteString(line.Text + "\n")
			}
			for _, test := range result.Tests {
				everything.WriteString(test.Name + " " + test.Message + "\n")
			}
			if err != nil {
				everything.WriteString(err.Error() + "\n")
				var scriptErr *Error
				if asError(err, &scriptErr) {
					everything.WriteString(scriptErr.Stack + "\n")
				}
			}

			output := everything.String()
			if strings.Contains(output, theSecret) {
				t.Fatalf("the secret leaked:\n%s", output)
			}
			if output == "" {
				t.Fatalf("nothing was produced, so nothing was exercised")
			}
			if attempt.mask && !strings.Contains(output, "[secret:apiKey]") {
				t.Errorf("no mask in the output:\n%s", output)
			}
		})
	}
}

// Enumerating a handle yields nothing at all. The properties are
// non-enumerable so its internals — including Go symbol names on the
// functions — never reach a script or the window.
func TestSecretHandleHasNoEnumerableShape(t *testing.T) {
	result, err := Run(secretOptions(t, `
		const h = secrets.ref("apiKey");
		console.log("keys", JSON.stringify(Object.keys(h)));
		console.log("spread", JSON.stringify({...h}));
	`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`keys []`, `spread {}`}
	for i, line := range result.Console {
		if i < len(want) && line.Text != want[i] {
			t.Errorf("console[%d] = %q, want %q", i, line.Text, want[i])
		}
	}
}

// A handle put on a header is stored as its mask, and the real value is
// available to the sender alone — which is what makes the wire work while the
// window still sees dots.
func TestSecretHandleOnAHeaderIsMaskedButSendable(t *testing.T) {
	opts := secretOptions(t, "")
	opts.Phase = Pre
	opts.Response = nil
	opts.Scripts = []Source{{Path: "orders/_pre.js", Code: `
		request.headers.set("Authorization", secrets.ref("apiKey").prefix("Bearer "));
		console.log("set to", request.headers.get("Authorization"));
	`}}

	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}

	// What the script and the window see.
	header := ""
	for _, h := range opts.Request.Headers {
		if h.Name == "Authorization" {
			header = h.Value
		}
	}
	if header != "Bearer [secret:apiKey]" {
		t.Errorf("the stored header = %q, want the mask", header)
	}
	for _, line := range result.Console {
		if strings.Contains(line.Text, theSecret) {
			t.Errorf("the console saw the value: %q", line.Text)
		}
	}

	// What the sender gets, and nothing else does.
	handle := result.Handles[HeaderSlot("Authorization")]
	if handle == nil {
		t.Fatal("the sender was given no handle for the header")
	}
	if got := handle.Reveal(); got != "Bearer "+theSecret {
		t.Errorf("Reveal = %q, want the real value", got)
	}
	if got := handle.String(); strings.Contains(got, theSecret) {
		t.Errorf("String() leaks: %q", got)
	}
}

// A secret must not be storable in a variable: a session value is shown in
// the folder view and an environment value is written to a committed file.
func TestSecretHandleCannotBeStoredInAVariable(t *testing.T) {
	for _, scope := range []string{"request", "session", "env"} {
		t.Run(scope, func(t *testing.T) {
			opts := secretOptions(t, `vars.`+scope+`.set("stolen", secrets.ref("apiKey"));`)
			result, err := Run(opts)
			if err == nil {
				t.Fatal("storing a secret in a variable should fail")
			}
			if !strings.Contains(err.Error(), "cannot be stored in a variable") {
				t.Errorf("error = %v, want it to say why", err)
			}
			if strings.Contains(err.Error(), theSecret) {
				t.Fatalf("the refusal leaked the value: %v", err)
			}
			store := opts.Vars.(*mapStore)
			if _, ok := store.ReadScope(VarScope(scope), "stolen"); ok {
				t.Error("the value was stored anyway")
			}
			_ = result
		})
	}
}

// A URL is recorded by every hop it passes, so a secret in one has left
// whatever Otis does afterwards.
func TestSecretHandleCannotBecomeTheURL(t *testing.T) {
	opts := secretOptions(t, "")
	opts.Phase = Pre
	opts.Response = nil
	opts.Scripts = []Source{{Path: "orders/_pre.js", Code: `request.url = secrets.ref("apiKey");`}}

	if _, err := Run(opts); err == nil {
		t.Fatal("a secret URL should be refused")
	} else if !strings.Contains(err.Error(), "recorded by every hop") {
		t.Errorf("error = %v, want it to say why", err)
	}
	if strings.Contains(opts.Request.URL, theSecret) {
		t.Fatal("the URL was set anyway")
	}
}

// secrets.ref is refused for anything that is not a declared secret, and the
// refusal names the key rather than any value.
func TestSecretRefRefusesWhatIsNotASecret(t *testing.T) {
	cases := map[string]string{
		"a plain environment value": `secrets.ref("host");`,
		"a name nothing declares":   `secrets.ref("nope");`,
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Run(secretOptions(t, code)); err == nil {
				t.Error("should be refused")
			} else if strings.Contains(err.Error(), theSecret) {
				t.Errorf("the refusal leaked: %v", err)
			}
		})
	}

	// And with no environment at all there is nothing to declare one.
	opts := secretOptions(t, `secrets.ref("apiKey");`)
	opts.Env = nil
	if _, err := Run(opts); err == nil {
		t.Error("secrets.ref with no active environment should be refused")
	}
}

// secrets.has answers a question with a safe answer.
func TestSecretHas(t *testing.T) {
	opts := secretOptions(t, `
		console.log("apiKey", secrets.has("apiKey"));
		console.log("host", secrets.has("host"));
		console.log("nope", secrets.has("nope"));
	`)
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apiKey true", "host false", "nope false"}
	for i, line := range result.Console {
		if i >= len(want) {
			break
		}
		if line.Text != want[i] {
			t.Errorf("console[%d] = %q, want %q", i, line.Text, want[i])
		}
	}
}

// asError is errors.As for *Error without the import.
func asError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
