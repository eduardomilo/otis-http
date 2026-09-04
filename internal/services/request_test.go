package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/importer/postman"
	"github.com/otis-http/otis/internal/resolve"
)

// newRequestService opens root as the current collection and returns a
// request service over it.
func newRequestService(t *testing.T, root string) *RequestService {
	t.Helper()
	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatalf("opening the collection: %v", err)
	}
	return NewRequestService(collections)
}

// inheritanceFixture is the shape screen 4a draws: a root folder file, a
// nested folder file, and a request that overrides one header and disables
// another.
func inheritanceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "_folder.http"), strings.Join([]string{
		"X-Tenant: acme",
		"Accept-Language: en-GB",
		"",
	}, "\n"))
	write(t, filepath.Join(root, "orders", "_folder.http"), strings.Join([]string{
		"# @auth bearer {{apiKey}}",
		"Accept: application/json",
		"Idempotency-Key: {{idemKey}}",
		"",
	}, "\n"))
	write(t, filepath.Join(root, "orders", "create-order.http"), strings.Join([]string{
		"# @name Create order",
		"POST {{baseUrl}}/v2/orders?expand=customer",
		"Content-Type: application/json",
		"Accept: application/vnd.acme+json",
		"X-Tenant: !inherit",
		"",
		`{"currency": "{{currency}}"}`,
		"",
	}, "\n"))
	write(t, filepath.Join(root, "env", "staging.json"),
		`{"baseUrl":"https://api.staging.acme.dev","apiKey":{"$secret":"keychain"},"currency":"usd"}`)
	return root
}

func TestRequestLoadInheritance(t *testing.T) {
	svc := newRequestService(t, inheritanceFixture(t))
	doc, err := svc.Load("orders/create-order.http", "staging")
	if err != nil {
		t.Fatal(err)
	}

	if doc.Name != "Create order" {
		t.Errorf("Name = %q, want %q", doc.Name, "Create order")
	}
	if !strings.Contains(doc.Raw, "POST {{baseUrl}}/v2/orders") {
		t.Errorf("Raw does not carry the file text:\n%s", doc.Raw)
	}
	if doc.File == nil || doc.File.Requests[doc.Index].Method != "POST" {
		t.Fatalf("parsed model missing the request line: %+v", doc.File)
	}

	// The chain is root-most first, the request last (§3).
	wantChain := []string{"_folder.http", "orders/_folder.http", "orders/create-order.http"}
	if got := doc.Chain; !equalStrings(got, wantChain) {
		t.Errorf("Chain = %v, want %v", got, wantChain)
	}

	// Effective headers: the request's Accept replaces the folder's, X-Tenant
	// is switched off, Accept-Language and Idempotency-Key come through.
	var effective []string
	for _, h := range doc.Effective.Headers {
		effective = append(effective, h.Name+": "+h.Value)
	}
	want := []string{
		"Accept-Language: en-GB",
		"Idempotency-Key: {{idemKey}}",
		"Content-Type: application/json",
		"Accept: application/vnd.acme+json",
	}
	if !equalStrings(effective, want) {
		t.Errorf("effective headers =\n  %v\nwant\n  %v", effective, want)
	}

	// The inherited list keeps the overridden and disabled entries, with what
	// became of them, so the UI can show and undo both.
	states := map[string]resolve.HeaderState{}
	sources := map[string]string{}
	for _, in := range doc.Inherited {
		states[in.Name] = in.State
		sources[in.Name] = in.Source.Path
	}
	for name, want := range map[string]resolve.HeaderState{
		"X-Tenant":        resolve.HeaderOff,
		"Accept-Language": resolve.HeaderSent,
		"Accept":          resolve.HeaderOverridden,
		"Idempotency-Key": resolve.HeaderSent,
	} {
		if states[name] != want {
			t.Errorf("inherited %s state = %q, want %q", name, states[name], want)
		}
	}
	if sources["Accept"] != "orders/_folder.http" {
		t.Errorf("inherited Accept source = %q, want orders/_folder.http", sources["Accept"])
	}
	if sources["X-Tenant"] != "_folder.http" {
		t.Errorf("inherited X-Tenant source = %q, want _folder.http", sources["X-Tenant"])
	}

	// Auth is inherited, so the AUTH row is not local, and it counts as sent.
	if doc.AuthHeader == nil {
		t.Fatal("AuthHeader is nil; the folder declares @auth bearer")
	}
	if doc.AuthHeader.Value != "Bearer {{apiKey}}" {
		t.Errorf("AuthHeader.Value = %q, want %q", doc.AuthHeader.Value, "Bearer {{apiKey}}")
	}
	if doc.AuthHeader.Local {
		t.Error("AuthHeader.Local is true; the @auth is in orders/_folder.http")
	}
	if doc.InheritedAuth == nil || doc.InheritedAuth.Kind != resolve.AuthBearer {
		t.Errorf("InheritedAuth = %+v, want a bearer auth", doc.InheritedAuth)
	}

	// 4 effective headers + the auth row = 5 sent; 2 of them local.
	wantCounts := Counts{Sent: 5, Local: 2, Inherited: 3}
	if doc.Counts != wantCounts {
		t.Errorf("Counts = %+v, want %+v", doc.Counts, wantCounts)
	}
	if doc.Counts.Sent != doc.Counts.Local+doc.Counts.Inherited {
		t.Errorf("counts do not add up: %+v", doc.Counts)
	}
}

func TestRequestLoadVariables(t *testing.T) {
	svc := newRequestService(t, inheritanceFixture(t))
	doc, err := svc.Load("orders/create-order.http", "staging")
	if err != nil {
		t.Fatal(err)
	}

	refs := map[string]VariableRef{}
	for _, v := range doc.Variables {
		refs[v.Name] = v
	}

	if got := refs["baseUrl"]; !got.Resolved || got.Value != "https://api.staging.acme.dev" {
		t.Errorf("baseUrl = %+v, want the staging value", got)
	}
	if got := refs["idemKey"]; got.Resolved {
		t.Errorf("idemKey = %+v, want unresolved (nothing defines it)", got)
	}
	if got := refs["currency"]; !got.Resolved || got.Value != "usd" {
		t.Errorf("currency = %+v, want usd from the environment", got)
	}
	// The secret resolves — the reference is satisfiable — but no value comes
	// back with it.
	got := refs["apiKey"]
	if !got.Secret {
		t.Errorf("apiKey = %+v, want Secret", got)
	}
	if got.Value != "" {
		t.Errorf("apiKey carries a value %q; a secret must never cross the binding", got.Value)
	}
}

// A secret must not leak through a file variable that refers to it: the
// variable *is* the secret under another name.
func TestRequestLoadSecretNeverLeaksThroughAVariable(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "get.http"), strings.Join([]string{
		"@token = {{apiKey}}",
		"@header = Bearer {{token}}",
		"GET https://example.test/me",
		"Authorization: {{header}}",
		"",
	}, "\n"))
	write(t, filepath.Join(root, "env", "staging.json"), `{"apiKey":{"$secret":"keychain"}}`)

	svc := newRequestService(t, root)
	doc, err := svc.Load("get.http", "staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range doc.Variables {
		if v.Name == "apiKey" || v.Name == "token" || v.Name == "header" {
			if !v.Secret {
				t.Errorf("%s = %+v, want Secret: it resolves to a keychain value", v.Name, v)
			}
			if v.Value != "" {
				t.Errorf("%s carries a value %q; anything resolving to a secret must carry none", v.Name, v.Value)
			}
		}
	}
}

func TestRequestSaveRoundTripsWithoutChangingBytes(t *testing.T) {
	root := inheritanceFixture(t)
	svc := newRequestService(t, root)
	path := "orders/create-order.http"

	doc, err := svc.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	before := doc.Raw

	saved, err := svc.Save(path, "", *doc.File)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Raw != before {
		t.Errorf("a no-op save changed the file:\n--- before\n%s\n--- after\n%s", before, saved.Raw)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "orders", "create-order.http"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != before {
		t.Errorf("bytes on disk differ from what was loaded:\n%q\nvs\n%q", string(onDisk), before)
	}
}

// The gate for increment 10: every request the Postman importer writes must
// survive a load-and-save with identical bytes. A file that changes on a no-op
// save means the serializer is wrong, and every such change would land in
// somebody's diff.
func TestRequestSaveRoundTripsEveryImportedRequest(t *testing.T) {
	export, err := os.ReadFile(filepath.Join("..", "importer", "postman", "testdata", "postman", "petstore.postman_collection.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := postman.Plan(export)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var requests []string
	for rel, content := range plan.Files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), content)
		if strings.HasSuffix(rel, ".http") && filepath.Base(rel) != "_folder.http" {
			requests = append(requests, rel)
		}
	}
	if len(requests) == 0 {
		t.Fatal("the import produced no request files")
	}

	svc := newRequestService(t, root)
	for _, rel := range requests {
		doc, err := svc.Load(rel, "")
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if doc.ParseError != "" {
			t.Errorf("%s: the importer wrote a file Otis cannot parse: %s", rel, doc.ParseError)
			continue
		}
		saved, err := svc.Save(rel, "", *doc.File)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if saved.Raw != doc.Raw {
			t.Errorf("%s: a no-op save rewrote the file:\n--- before\n%s\n--- after\n%s", rel, doc.Raw, saved.Raw)
		}
	}
}

func TestRequestSaveWritesOverrideAndInheritMarker(t *testing.T) {
	svc := newRequestService(t, inheritanceFixture(t))
	path := "orders/create-order.http"
	doc, err := svc.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}

	// Override the inherited Idempotency-Key, and switch Accept-Language off.
	file := *doc.File
	entry := file.Requests[doc.Index]
	entry.Headers = append(entry.Headers,
		httpfile.Header{Name: "Idempotency-Key", Value: "{{idemKey}}"},
		httpfile.Header{Name: "Accept-Language", Value: resolve.InheritMarker},
	)
	saved, err := svc.Save(path, "", file)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(saved.Raw, "Idempotency-Key: {{idemKey}}") {
		t.Errorf("the override was not written:\n%s", saved.Raw)
	}
	if !strings.Contains(saved.Raw, "Accept-Language: !inherit") {
		t.Errorf("the !inherit marker was not written:\n%s", saved.Raw)
	}

	states := map[string]resolve.HeaderState{}
	for _, in := range saved.Inherited {
		states[in.Name] = in.State
	}
	if states["Idempotency-Key"] != resolve.HeaderOverridden {
		t.Errorf("Idempotency-Key state = %q, want overridden", states["Idempotency-Key"])
	}
	if states["Accept-Language"] != resolve.HeaderOff {
		t.Errorf("Accept-Language state = %q, want off", states["Accept-Language"])
	}

	// Accept-Language is no longer sent; Idempotency-Key moved to local.
	for _, h := range saved.Effective.Headers {
		if h.Name == "Accept-Language" {
			t.Errorf("Accept-Language is still sent after !inherit: %+v", h)
		}
	}
	// Nothing is inherited as a header any more — Accept and Idempotency-Key
	// are overridden, X-Tenant and Accept-Language are off — so the only
	// inherited entry left is the auth row.
	wantCounts := Counts{Sent: 4, Local: 3, Inherited: 1}
	if saved.Counts != wantCounts {
		t.Errorf("Counts = %+v, want %+v", saved.Counts, wantCounts)
	}
}

func TestRequestSaveAuthOptions(t *testing.T) {
	path := "orders/create-order.http"
	cases := []struct {
		name      string
		directive *httpfile.Directive
		wantLine  string
		absent    string
		wantKind  resolve.AuthKind
		wantLocal bool
	}{
		{
			name:      "inherit writes nothing",
			wantLine:  "",
			absent:    "@auth",
			wantKind:  resolve.AuthBearer,
			wantLocal: false,
		},
		{
			name:      "override writes an @auth block",
			directive: &httpfile.Directive{Style: "#", Name: "auth", Value: "bearer {{overrideToken}}"},
			wantLine:  "# @auth bearer {{overrideToken}}",
			wantKind:  resolve.AuthBearer,
			wantLocal: true,
		},
		{
			name:      "no auth writes @auth none",
			directive: &httpfile.Directive{Style: "#", Name: "auth", Value: "none"},
			wantLine:  "# @auth none",
			wantKind:  resolve.AuthNone,
			wantLocal: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newRequestService(t, inheritanceFixture(t))
			doc, err := svc.Load(path, "")
			if err != nil {
				t.Fatal(err)
			}
			file := *doc.File
			if tc.directive != nil {
				file.Requests[doc.Index].Directives = append(file.Requests[doc.Index].Directives, *tc.directive)
			}
			saved, err := svc.Save(path, "", file)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLine != "" && !strings.Contains(saved.Raw, tc.wantLine) {
				t.Errorf("want %q in the file:\n%s", tc.wantLine, saved.Raw)
			}
			if tc.absent != "" && strings.Contains(saved.Raw, tc.absent) {
				t.Errorf("want no %q in the file:\n%s", tc.absent, saved.Raw)
			}
			if saved.Effective.Auth == nil || saved.Effective.Auth.Kind != tc.wantKind {
				t.Fatalf("effective auth = %+v, want kind %q", saved.Effective.Auth, tc.wantKind)
			}
			local := saved.Effective.Auth.Source.Path == path
			if local != tc.wantLocal {
				t.Errorf("auth local = %v, want %v (source %s)", local, tc.wantLocal, saved.Effective.Auth.Source.Path)
			}
			// "No auth" sends no Authorization at all, so there is no AUTH row.
			if tc.wantKind == resolve.AuthNone && saved.AuthHeader != nil {
				t.Errorf("AuthHeader = %+v, want nil for @auth none", saved.AuthHeader)
			}
			// Whichever option was chosen, the folder's auth is still what
			// "Inherit from folder" would show and what Override prefills from.
			if saved.InheritedAuth == nil || saved.InheritedAuth.Token != "{{apiKey}}" {
				t.Errorf("InheritedAuth = %+v, want the folder's bearer {{apiKey}}", saved.InheritedAuth)
			}
		})
	}
}

// An explicit Authorization header beats @auth (§3.3), so no AUTH row is
// claimed and the count does not double.
func TestRequestAuthHeaderYieldsToAnExplicitHeader(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "_folder.http"), "# @auth bearer {{apiKey}}\n")
	write(t, filepath.Join(root, "get.http"), "GET https://example.test/me\nAuthorization: Custom abc\n")

	svc := newRequestService(t, root)
	doc, err := svc.Load("get.http", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.AuthHeader != nil {
		t.Errorf("AuthHeader = %+v, want nil: the request sends its own Authorization", doc.AuthHeader)
	}
	if doc.Counts != (Counts{Sent: 1, Local: 1}) {
		t.Errorf("Counts = %+v, want one local header and nothing else", doc.Counts)
	}
}

func TestRequestLoadBrokenFileStillOpens(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "broken.http"), "GET https://example.test/ nonsense trailing\n")

	svc := newRequestService(t, root)
	doc, err := svc.Load("broken.http", "")
	if err != nil {
		t.Fatalf("a broken file must still open: %v", err)
	}
	if doc.ParseError == "" {
		t.Error("ParseError is empty for a file that does not parse")
	}
	if doc.Raw == "" {
		t.Error("Raw is empty; the text is the only way to repair the file")
	}
}

func TestRequestSaveTextRefusesUnparseableText(t *testing.T) {
	svc := newRequestService(t, inheritanceFixture(t))
	if _, err := svc.SaveText("orders/create-order.http", "", "GET https://example.test/ junk trailing\n"); err == nil {
		t.Fatal("SaveText accepted text that does not parse")
	}
}

func TestRequestSaveRefusesPathsOutsideTheCollection(t *testing.T) {
	svc := newRequestService(t, inheritanceFixture(t))
	if _, err := svc.Save("../escape.http", "", httpfile.File{}); err == nil {
		t.Fatal("Save accepted a path outside the collection")
	}
	if _, err := svc.Save("notes.md", "", httpfile.File{}); err == nil {
		t.Fatal("Save accepted a file that is not a request")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Creating a request: the file is named for the slug, the typed name survives
// as the @name directive, and `.order` is not touched.
func TestCreateRequest(t *testing.T) {
	root := inheritanceFixture(t)
	s := newRequestService(t, root)

	nodePath, err := s.Create("orders", "Refund order")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if nodePath != "orders/refund-order.http" {
		t.Errorf("nodePath = %q, want orders/refund-order.http", nodePath)
	}

	body := readFile(t, filepath.Join(root, "orders", "refund-order.http"))
	if !strings.Contains(body, "# @name Refund order") {
		t.Errorf("the typed name is not in the file:\n%s", body)
	}
	// It has to parse, or the editor cannot open what was just created.
	if _, err := httpfile.ParseString(body); err != nil {
		t.Errorf("the created file does not parse: %v\n%s", err, body)
	}

	// And the tree shows it under the name that was typed, not the slug
	// (docs/FORMAT.md §2.1 prefers @name).
	loaded, err := s.collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find(nodePath)
	if node == nil {
		t.Fatal("the created request is not in the tree")
	}
	if node.Name != "Refund order" {
		t.Errorf("node name = %q, want %q", node.Name, "Refund order")
	}
}

// A name that already exists gets -2 rather than an error: the name a person
// types is a label, and two requests may reasonably want the same one.
func TestCreateRequestResolvesCollisions(t *testing.T) {
	s := newRequestService(t, inheritanceFixture(t))

	first, err := s.Create("orders", "Duplicate")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("orders", "Duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if first != "orders/duplicate.http" || second != "orders/duplicate-2.http" {
		t.Errorf("got %q then %q, want duplicate.http then duplicate-2.http", first, second)
	}
}

// A name with nothing usable in it still produces a file.
func TestCreateRequestFallsBackWhenTheNameSlugsToNothing(t *testing.T) {
	s := newRequestService(t, inheritanceFixture(t))
	got, err := s.Create("", "***")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != "request.http" {
		t.Errorf("nodePath = %q, want request.http", got)
	}
}

func TestCreateRequestRejectsSomethingThatIsNotAFolder(t *testing.T) {
	s := newRequestService(t, inheritanceFixture(t))
	if _, err := s.Create("orders/create-order.http", "Nope"); err == nil {
		t.Error("Create accepted a request as its parent folder")
	}
	if _, err := s.Create("nowhere", "Nope"); err == nil {
		t.Error("Create accepted a folder that does not exist")
	}
}
