package resolve

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/secrets"
)

// fixedScope makes builtins deterministic.
func fixedScope(s *Scope) {
	s.WithClock(func() time.Time { return time.Date(2026, 9, 2, 15, 4, 5, 0, time.UTC) })
	n := 0
	s.WithRandom(
		func() string { n++; return "uuid-" + strings.Repeat("0", 3) + string(rune('0'+n)) },
		func(max int) int { return max },
	)
}

func TestResolveRequest(t *testing.T) {
	const req = "api/users/create.http"
	env := &Environment{Name: "dev", Path: "env/dev.json", Values: map[string]EnvValue{
		"host":   {Value: "dev.example.com"},
		"scheme": {Value: "https"},
		"token":  {Secret: true},
		"shadow": {Value: "from-env"},
	}}
	store := secrets.NewMemory()
	_ = store.Set(secrets.Key("mycol", "dev", "token"), "s3cr3t-token")

	tests := []struct {
		name    string
		files   map[string]string
		env     *Environment
		store   secrets.Store
		check   func(t *testing.T, r *Resolved)
		wantErr string
	}{
		{
			name: "scopes: request var beats folder beats env; folder nearest first",
			files: map[string]string{
				"_folder.http":     "@shadow = from-root\n@rootOnly = R\n@base = {{scheme}}://{{host}}\n",
				"api/_folder.http": "@shadow = from-api\n@apiOnly = A\n",
				req:                "@shadow = from-request\nPOST {{base}}/users?r={{rootOnly}}&a={{apiOnly}}&s={{shadow}}\n",
			},
			env: env,
			check: func(t *testing.T, r *Resolved) {
				if r.URL != "https://dev.example.com/users?r=R&a=A&s=from-request" {
					t.Errorf("url = %q", r.URL)
				}
				wantUses := []Use{
					{Name: "base", Origin: OriginFolder, Source: Source{"_folder.http", 3}, Value: "https://dev.example.com"},
					{Name: "scheme", Origin: OriginEnv, Source: Source{Path: "env/dev.json"}, Value: "https"},
					{Name: "host", Origin: OriginEnv, Source: Source{Path: "env/dev.json"}, Value: "dev.example.com"},
					{Name: "rootOnly", Origin: OriginFolder, Source: Source{"_folder.http", 2}, Value: "R"},
					{Name: "apiOnly", Origin: OriginFolder, Source: Source{"api/_folder.http", 2}, Value: "A"},
					{Name: "shadow", Origin: OriginRequest, Source: Source{req, 1}, Value: "from-request"},
				}
				if !reflect.DeepEqual(r.Variables, wantUses) {
					t.Errorf("uses =\n%+v\nwant\n%+v", r.Variables, wantUses)
				}
			},
		},
		{
			name: "env shadowed by folder when request does not define it",
			files: map[string]string{
				"_folder.http": "@shadow = from-root\n",
				req:            "GET https://x.test/{{shadow}}\n",
			},
			env: env,
			check: func(t *testing.T, r *Resolved) {
				if r.URL != "https://x.test/from-root" {
					t.Errorf("url = %q", r.URL)
				}
			},
		},
		{
			name: "headers, auth and body are resolved; file body path is not",
			files: map[string]string{
				"_folder.http": "@v = 1\n# @auth basic {{user}} {{pass}}\nX-Version: {{v}}\nAccept: {{ accept }}\n",
				"api/create.http": "@user = alice\n@pass = pa ss\n@accept = application/json\n@id = 42\n" +
					"POST https://x.test\nX-Req: {{id}}-{{v}}\n\n{\"id\": {{id}}, \"path\": \"{{v}}/{{unknownInLiteral\"}\n",
			},
			check: func(t *testing.T, r *Resolved) {
				wantH := []Header{
					{"X-Version", "1", Source{"_folder.http", 3}},
					{"Accept", "application/json", Source{"_folder.http", 4}},
					{"X-Req", "42-1", Source{"api/create.http", 6}},
				}
				if !reflect.DeepEqual(r.Headers, wantH) {
					t.Errorf("headers = %+v", r.Headers)
				}
				if r.Auth == nil || r.Auth.Username != "alice" || r.Auth.Password != "pa ss" {
					t.Errorf("auth = %+v", r.Auth)
				}
				// "{{unknownInLiteral\"}" is not a well-formed reference and stays literal.
				if r.Body.Raw != "{\"id\": 42, \"path\": \"1/{{unknownInLiteral\"}" {
					t.Errorf("body = %q", r.Body.Raw)
				}
			},
		},
		{
			name: "body file path is left untouched",
			files: map[string]string{
				req: "@dir = payloads\nPOST https://x.test\n\n<@ ./{{dir}}/a.json\n",
			},
			check: func(t *testing.T, r *Resolved) {
				if r.Body.FilePath != "./{{dir}}/a.json" || !r.Body.SubstituteVariables {
					t.Errorf("body = %+v", r.Body)
				}
			},
		},
		{
			name: "recursive values and inner whitespace",
			files: map[string]string{
				"_folder.http": "@a = A-{{ b }}\n@b = B-{{c}}\n@c = C\n",
				req:            "GET https://x.test/{{a}}/{{a}}\n",
			},
			check: func(t *testing.T, r *Resolved) {
				if r.URL != "https://x.test/A-B-C/A-B-C" {
					t.Errorf("url = %q", r.URL)
				}
				if len(r.Variables) != 3 {
					t.Errorf("uses deduplicated = %d, want 3: %+v", len(r.Variables), r.Variables)
				}
			},
		},
		{
			name: "last declaration in a file wins",
			files: map[string]string{
				req: "@x = first\n@x = second\nGET https://x.test/{{x}}\n",
			},
			check: func(t *testing.T, r *Resolved) {
				if r.URL != "https://x.test/second" {
					t.Errorf("url = %q", r.URL)
				}
			},
		},
		{
			name: "builtins: fresh per occurrence, recorded per occurrence",
			files: map[string]string{
				req: "POST https://x.test/{{$uuid}}\nX-Id: {{$uuid}}\nX-T: {{$timestamp}}\nX-Iso: {{$isoTimestamp}}\nX-R: {{$randomInt}}\n",
			},
			check: func(t *testing.T, r *Resolved) {
				if r.URL != "https://x.test/uuid-0001" {
					t.Errorf("url = %q", r.URL)
				}
				want := map[string]string{"X-Id": "uuid-0002", "X-T": "1788361445", "X-Iso": "2026-09-02T15:04:05Z", "X-R": "1000"}
				for _, h := range r.Headers {
					if want[h.Name] != h.Value {
						t.Errorf("%s = %q, want %q", h.Name, h.Value, want[h.Name])
					}
				}
				if len(r.Variables) != 5 || r.Variables[0].Origin != OriginBuiltin || r.Variables[0].Source.Path != "builtin" {
					t.Errorf("uses = %+v", r.Variables)
				}
			},
		},
		{
			name: "secret resolved from store; value never in Variables; Mask hides it",
			files: map[string]string{
				req: "# @auth bearer {{token}}\nGET https://x.test\nX-Token: {{token}}\n\n{\"t\": \"{{token}}\"}\n",
			},
			env: env, store: store,
			check: func(t *testing.T, r *Resolved) {
				if r.Auth.Token != "s3cr3t-token" || r.Headers[0].Value != "s3cr3t-token" || r.Body.Raw != "{\"t\": \"s3cr3t-token\"}" {
					t.Errorf("secret not substituted: %+v", r)
				}
				if !r.HasSecrets() {
					t.Error("HasSecrets = false")
				}
				want := []Use{{Name: "token", Origin: OriginEnv, Source: Source{Path: "env/dev.json"}, Secret: true}}
				if !reflect.DeepEqual(r.Variables, want) {
					t.Errorf("uses = %+v", r.Variables)
				}
				if got := r.Mask("Bearer s3cr3t-token and s3cr3t-token"); got != "Bearer ••••• and •••••" {
					t.Errorf("Mask = %q", got)
				}
			},
		},
		{
			name: "missing: every unresolved name reported once, in first-use order",
			files: map[string]string{
				"_folder.http": "@a = {{deep}}\n",
				req:            "GET https://{{h}}/{{p}}\nX: {{h}} {{$nope}} {{a}}\n\n{{p}}\n",
			},
			wantErr: "unresolved variables: h, p, $nope, deep",
		},
		{
			name: "cycle",
			files: map[string]string{
				req: "@a = {{b}}\n@b = {{c}}\n@c = {{a}}\nGET https://x.test/{{a}}\n",
			},
			wantErr: "variable cycle: a -> b -> c -> a",
		},
		{
			name: "self cycle",
			files: map[string]string{
				req: "@a = x{{a}}\nGET https://x.test/{{a}}\n",
			},
			wantErr: "variable cycle: a -> a",
		},
		{
			name:    "secret not in store: error names key, not value",
			files:   map[string]string{req: "GET https://x.test/{{token}}\n"},
			env:     env,
			store:   secrets.NewMemory(),
			wantErr: `secret "token" (mycol/dev/token) is not set in the secret store`,
		},
		{
			name:    "secret without a store",
			files:   map[string]string{req: "GET https://x.test/{{token}}\n"},
			env:     env,
			wantErr: `secret "token" (mycol/dev/token): no secret store available`,
		},
		{
			name:    "auth error from inheritance propagates",
			files:   map[string]string{"_folder.http": "# @auth bearer\n", req: "GET https://x.test\n"},
			wantErr: "_folder.http:1: @auth bearer takes exactly one token, got 0 arguments",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tree(t, tt.files)
			var node = c.Find(req)
			if node == nil {
				node = c.Requests()[0]
			}
			r, err := Request(node, Options{Env: tt.env, Secrets: tt.store, Collection: "mycol", Configure: fixedScope})
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, r)
		})
	}
}

func TestErrorTypes(t *testing.T) {
	c := tree(t, map[string]string{"r.http": "GET https://x.test/{{a}}/{{b}}\n"})
	_, err := Request(c.Find("r.http"), Options{})
	var me *MissingError
	if !errors.As(err, &me) || !reflect.DeepEqual(me.Names, []string{"a", "b"}) {
		t.Errorf("err = %v", err)
	}

	env := &Environment{Name: "e", Path: "env/e.json", Values: map[string]EnvValue{"s": {Secret: true}}}
	c = tree(t, map[string]string{"r.http": "GET https://x.test/{{s}}\n"})
	_, err = Request(c.Find("r.http"), Options{Env: env, Secrets: secrets.NewMemory(), Collection: "k"})
	var se *SecretError
	if !errors.As(err, &se) || !errors.Is(err, secrets.ErrNotFound) || se.Key != "k/e/s" {
		t.Errorf("err = %v", err)
	}
}

func TestInCollectionDefaultsKey(t *testing.T) {
	c := tree(t, map[string]string{"r.http": "GET https://x.test/{{s}}\n"})
	env := &Environment{Name: "e", Path: "env/e.json", Values: map[string]EnvValue{"s": {Secret: true}}}
	store := secrets.NewMemory()
	_ = store.Set(secrets.Key(CollectionKey(c), "e", "s"), "v")
	r, err := InCollection(c, c.Find("r.http"), Options{Env: env, Secrets: store})
	if err != nil || r.URL != "https://x.test/v" {
		t.Errorf("r = %+v, err = %v", r, err)
	}
}

func TestRequestRejectsNonRequests(t *testing.T) {
	c := tree(t, map[string]string{"a/r.http": "GET https://x.test\n"})
	if _, err := Request(c.Find("a"), Options{}); err == nil {
		t.Error("folder accepted")
	}
	if _, err := Request(nil, Options{}); err == nil {
		t.Error("nil accepted")
	}
}

func TestRealBuiltins(t *testing.T) {
	c := tree(t, map[string]string{"r.http": "GET https://x.test/{{$uuid}}/{{$randomInt}}/{{$timestamp}}/{{$isoTimestamp}}\n"})
	r, err := Request(c.Find("r.http"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(r.URL, "https://x.test/"), "/")
	if len(parts) != 4 {
		t.Fatalf("url = %q", r.URL)
	}
	if len(parts[0]) != 36 || parts[0][14] != '4' {
		t.Errorf("uuid = %q", parts[0])
	}
	if n, err := time.Parse(time.RFC3339, parts[3]); err != nil || time.Since(n) > time.Minute {
		t.Errorf("isoTimestamp = %q (%v)", parts[3], err)
	}
	if ri, err := strconv.Atoi(parts[1]); err != nil || ri < 0 || ri > 1000 {
		t.Errorf("randomInt = %q", parts[1])
	}
}
