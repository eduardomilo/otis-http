package services

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// The window is shown the file it will get, produced by the serializer that
// will write it — not a rendering of a model one step away from the truth.
func TestPlanCurlShowsTheFileItWillWrite(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "existing.http"), "GET https://x.test/\n")
	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	svc := NewRequestService(collections)

	plan := svc.PlanCurl(`curl -X POST 'https://api.test/v2/orders' -H 'Content-Type: application/json' --data-raw '{"a":1}' -k`)
	if plan.Problem != "" {
		t.Fatalf("problem = %q", plan.Problem)
	}
	for _, want := range []string{"# @name Post orders", "POST https://api.test/v2/orders", `{"a":1}`, "--insecure"} {
		if !strings.Contains(plan.Text, want) {
			t.Errorf("the plan does not contain %q:\n%s", want, plan.Text)
		}
	}
	if len(plan.Notes) != 1 {
		t.Errorf("notes = %q", plan.Notes)
	}

	// An empty box is not an error, and neither is a half-typed command: this
	// runs on every keystroke.
	if got := svc.PlanCurl("   "); got.Problem != "" || got.Text != "" {
		t.Errorf("an empty command reported %+v", got)
	}
	if got := svc.PlanCurl("curl 'https://x"); got.Problem == "" {
		t.Error("an unbalanced quote reported no problem")
	}
}

func TestCreateFromCurlWritesARequest(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "existing.http"), "GET https://x.test/\n")
	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	svc := NewRequestService(collections)

	nodePath, err := svc.CreateFromCurl("orders", "Create order", `curl -X POST https://api.test/v2/orders -d 'a=1'`)
	if err != nil {
		t.Fatal(err)
	}
	// The typed name names the file, exactly as it does for Create.
	if nodePath != "orders/create-order.http" {
		t.Errorf("nodePath = %q", nodePath)
	}
	doc, err := svc.Load(nodePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.File == nil || len(doc.File.Requests) == 0 {
		t.Fatal("the written file has no request")
	}
	entry := doc.File.Requests[0]
	if entry.Method != "POST" || entry.URL != "https://api.test/v2/orders" {
		t.Errorf("entry = %s %s", entry.Method, entry.URL)
	}
	if got := entry.Name(); got != "Create order" {
		t.Errorf("name = %q, want the typed one", got)
	}

	// A second import of the same thing does not overwrite the first.
	again, err := svc.CreateFromCurl("orders", "Create order", `curl https://api.test/v2/orders`)
	if err != nil {
		t.Fatal(err)
	}
	if again == nodePath {
		t.Errorf("the second import overwrote the first at %q", again)
	}
}

func TestCreateFromCurlRefusesWhatIsNotACommand(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.http"), "GET https://x.test/\n")
	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	svc := NewRequestService(collections)

	if _, err := svc.CreateFromCurl("", "x", "curl -X POST"); err == nil {
		t.Error("a command with no URL was accepted")
	}
	if _, err := svc.CreateFromCurl("nope", "x", "curl https://api.test/"); err == nil {
		t.Error("a folder that is not in the collection was accepted")
	}
}

// The masked form is what makes "copy as cURL" safe to paste into an issue,
// and the secret it masks is one the command would otherwise carry in full.
// Both are legitimate; what must not happen is the wrong one.
func TestCopyAsCurlMasksUnlessAskedNotTo(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "env", "dev.json"),
		`{"baseUrl": "https://api.test", "apiKey": {"$secret": "keychain"}}`)
	write(t, filepath.Join(root, "ping.http"),
		"# @name Ping\n# @auth bearer {{apiKey}}\nGET {{baseUrl}}/ping\n")

	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	store := secrets.NewMemory()
	if err := store.Set(secrets.Key(resolve.CollectionKey(loaded), "dev", "apiKey"), "sk-live-abc123"); err != nil {
		t.Fatal(err)
	}
	sends := NewSendService(collections, store)

	masked, err := sends.curlCommand("ping.http", "dev", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(masked, "sk-live-abc123") {
		t.Errorf("the masked command carries the secret:\n%s", masked)
	}
	if !strings.Contains(masked, "Authorization: Bearer") {
		t.Errorf("the masked command lost the header it was masking:\n%s", masked)
	}

	real, err := sends.curlCommand("ping.http", "dev", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(real, "sk-live-abc123") {
		t.Errorf("the runnable command has no credential in it:\n%s", real)
	}

	// Both are the request as it will be *sent*: the environment's host, and
	// the header an @auth line becomes — neither of which is in the file.
	for _, command := range []string{masked, real} {
		if !strings.Contains(command, "https://api.test/ping") {
			t.Errorf("{{baseUrl}} was not resolved:\n%s", command)
		}
		if strings.Contains(command, "{{") {
			t.Errorf("a reference survived into the command:\n%s", command)
		}
	}
}

// A pre-request script can change the request, and the command is built
// without running one — so the command says so rather than being quietly
// different from what a send would do.
func TestCopyAsCurlSaysWhenAScriptWouldHaveRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ping.http"), "GET https://api.test/ping\n")
	write(t, filepath.Join(root, "_pre.js"), "request.headers.set('X-Run', '1');\n")

	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	sends := NewSendService(collections, secrets.NewMemory())

	command, err := sends.curlCommand("ping.http", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "pre-request script") {
		t.Errorf("no note about the hook:\n%s", command)
	}
}
