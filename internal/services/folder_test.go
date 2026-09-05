package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// folderFixture is the design's own collection: orders/ with shared settings,
// a README, two hooks, a lib/ of modules, five requests and one subfolder
// whose request opts out of the folder's auth.
func folderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Variables are preamble and come before the headers (docs/FORMAT.md §1.1).
	write(t, filepath.Join(root, "_folder.http"), "@apiBase = https://api.example.test\nX-Client: otis\n")
	write(t, filepath.Join(root, "orders", "_folder.http"),
		"# @auth bearer {{apiKey}}\n@currency = usd\n@expand = customer\nAccept: application/json\nIdempotency-Key: {{idemKey}}\n")
	write(t, filepath.Join(root, "orders", "README.md"), "# Orders\n\nCreate, read and mutate orders.\n")
	write(t, filepath.Join(root, "orders", "_pre.js"),
		"import { idempotencyKey } from \"../lib/idempotency.js\";\nconst key = idempotencyKey();\nvars.request.set(\"idemKey\", key);\n")
	write(t, filepath.Join(root, "orders", "_post.js"),
		"if (response.status === 201)\n  vars.folder.set(\"orderId\", response.body.id);\n")
	write(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{apiBase}}/orders\n")
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nPOST {{apiBase}}/orders\n")
	write(t, filepath.Join(root, "orders", "get-order.http"), "GET {{apiBase}}/orders/1\n")
	write(t, filepath.Join(root, "orders", "update-status.http"), "PATCH {{apiBase}}/orders/1\n")
	write(t, filepath.Join(root, "orders", "cancel-order.http"),
		"DELETE {{apiBase}}/orders/1\nAccept: !inherit\n")
	// The subfolder's request opts out of the folder's auth (screen 3a's
	// Overrides row) and lives one level deeper, so it counts towards "below"
	// but not towards the direct count.
	write(t, filepath.Join(root, "orders", "fixtures", "seed-order.http"),
		"# @auth none\nPOST {{apiBase}}/orders?seed=1\n")
	write(t, filepath.Join(root, "lib", "idempotency.js"), "export function idempotencyKey() {}\n")
	write(t, filepath.Join(root, "lib", "assert.js"), "export function ok() {}\n")
	write(t, filepath.Join(root, "env", "staging.json"), `{"apiKey": {"$secret": "keychain"}}`)
	return root
}

func newFolderService(t *testing.T) (*FolderService, *SendService, *CollectionService, string) {
	t.Helper()
	root := folderFixture(t)
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	sends := NewSendService(collections, secrets.NewMemory())
	return NewFolderService(collections, sends), sends, collections, root
}

// DESIGN-NOTES §9.6, resolved: a count describing the folder's *contents* is
// direct, a count describing its *reach* is recursive, and the labels say
// which. Screen 3a's "5 requests · 1 subfolder" and "inherited by 6 requests"
// are both right and are both here.
func TestFolderCountsSeparateContentsFromReach(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Counts.Requests != 5 {
		t.Errorf("direct requests = %d, want 5", doc.Counts.Requests)
	}
	if doc.Counts.Subfolders != 1 {
		t.Errorf("subfolders = %d, want 1", doc.Counts.Subfolders)
	}
	if doc.Counts.Below != 6 {
		t.Errorf("requests below = %d, want 6 (including fixtures/)", doc.Counts.Below)
	}
	if doc.Counts.Scripts != 2 {
		t.Errorf("scripts = %d, want the two hooks", doc.Counts.Scripts)
	}
	if len(doc.Inheriting) != 6 {
		t.Errorf("inheriting = %v, want 6", doc.Inheriting)
	}
	// In display order, so a Run folder follows .order.
	if doc.Inheriting[0] != "orders/cancel-order.http" {
		t.Errorf("inheriting starts at %q; want display order", doc.Inheriting[0])
	}
}

func TestFolderLoadsSettingsAndReadme(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != "orders" || doc.Name != "orders" {
		t.Errorf("doc = %+v", doc)
	}
	if doc.SettingsPath != "orders/_folder.http" || doc.Settings == nil {
		t.Errorf("settings = %q %v", doc.SettingsPath, doc.Settings)
	}
	if doc.ReadmePath != "orders/README.md" || !strings.HasPrefix(doc.Readme, "# Orders") {
		t.Errorf("readme = %q %q", doc.ReadmePath, doc.Readme)
	}
}

// The root is a folder too, and its name is the collection's rather than the
// directory's — a root called ".requests" would be useless.
func TestFolderLoadsTheRoot(t *testing.T) {
	svc, _, _, root := newFolderService(t)
	doc, err := svc.Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != "" {
		t.Errorf("path = %q, want empty for the root", doc.Path)
	}
	if doc.Name != collection.DisplayName(root) {
		t.Errorf("name = %q, want the collection's display name", doc.Name)
	}
	if doc.Counts.Below != 6 {
		t.Errorf("requests below the root = %d, want 6", doc.Counts.Below)
	}
}

// Auth and headers are what every request below *starts with*, so an
// ancestor's contribution is there too and each row names where it came from.
func TestFolderAuthAndHeadersCarryProvenance(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "staging")
	if err != nil {
		t.Fatal(err)
	}

	if doc.Auth == nil {
		t.Fatal("no auth")
	}
	if doc.Auth.Kind != resolve.AuthBearer || doc.Auth.Summary != "Bearer token" {
		t.Errorf("auth = %+v", doc.Auth)
	}
	if doc.Auth.Token != "{{apiKey}}" {
		t.Errorf("token = %q, want the reference as written", doc.Auth.Token)
	}
	if !doc.Auth.Local || doc.Auth.Source.Path != "orders/_folder.http" {
		t.Errorf("auth source = %+v, local = %v", doc.Auth.Source, doc.Auth.Local)
	}
	// The token resolves to a secret, so the panel can show the keychain
	// lock. The value itself is never fetched: the read path uses
	// secrets.Placeholder.
	if !doc.Auth.Secret {
		t.Error("the token resolves through a secret and should say so")
	}
	if !strings.Contains(doc.Auth.Sends, resolve.MaskPlaceholder) {
		t.Errorf("Sends = %q, want it masked", doc.Auth.Sends)
	}

	byName := map[string]FolderHeader{}
	for _, h := range doc.Headers {
		byName[h.Name] = h
	}
	if h := byName["Accept"]; h.Value != "application/json" || !h.Local {
		t.Errorf("Accept = %+v, want the folder's own", h)
	}
	if h := byName["X-Client"]; h.Value != "otis" || h.Local {
		t.Errorf("X-Client = %+v, want inherited from the root", h)
	}
	if h := byName["X-Client"]; h.Source.Path != "_folder.http" {
		t.Errorf("X-Client source = %+v", h.Source)
	}
}

// Committed variables come from this folder and its ancestors, nearest wins,
// and each says where it is written.
func TestFolderVariablesAreCommittedWithProvenance(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]FolderVariable{}
	for _, v := range doc.Variables {
		byName[v.Name] = v
	}
	if v := byName["currency"]; v.Value != "usd" || !v.Local {
		t.Errorf("currency = %+v", v)
	}
	if v := byName["apiBase"]; v.Value != "https://api.example.test" || v.Local {
		t.Errorf("apiBase = %+v, want inherited from the root", v)
	}
	if v := byName["apiBase"]; v.Source.Path != "_folder.http" {
		t.Errorf("apiBase source = %+v", v.Source)
	}
}

// The two variable groups are separate things and the service keeps them
// separate: Committed is on disk and shared, Session is in memory and this
// machine's only (docs/FORMAT.md §4.5).
func TestFolderSessionVariablesAreTheirOwnGroup(t *testing.T) {
	svc, sends, _, _ := newFolderService(t)

	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Session) != 0 {
		t.Errorf("session = %+v, want none before a run sets one", doc.Session)
	}

	// A run sets one. The writer arrives with the script engine (Increment
	// 15); the store, the scope and this read surface are Increment 11's.
	sends.varsStore().Set(resolve.SessionValue{
		Scope: resolve.SessionFolder, Owner: "orders",
		Name: "orderId", Value: "ord_9Xk2mP4qL7",
		Origin: "orders/create-order.http", At: time.Now(),
	})
	// And one for a different folder, which must not appear here.
	sends.varsStore().Set(resolve.SessionValue{
		Scope: resolve.SessionFolder, Owner: "lib",
		Name: "elsewhere", Value: "no", At: time.Now(),
	})
	// And one for the environment scope, which is not a folder's.
	sends.varsStore().Set(resolve.SessionValue{
		Scope: resolve.SessionEnv, Owner: "staging",
		Name: "cursor", Value: "no", At: time.Now(),
	})

	doc, err = svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Session) != 1 {
		t.Fatalf("session = %+v, want only this folder's", doc.Session)
	}
	got := doc.Session[0]
	if got.Name != "orderId" || got.Value != "ord_9Xk2mP4qL7" {
		t.Errorf("session value = %+v", got)
	}
	// Provenance is the whole account of a value that is in no file.
	if got.Origin != "orders/create-order.http" || got.At.IsZero() {
		t.Errorf("session provenance = %+v", got)
	}
	// A session variable never appears among the committed ones.
	for _, v := range doc.Variables {
		if v.Name == "orderId" {
			t.Error("a session variable leaked into the committed group")
		}
	}

	// Clear forgets this folder's and leaves the others.
	doc, err = svc.ClearSession("orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Session) != 0 {
		t.Errorf("session after Clear = %+v", doc.Session)
	}
	if len(sends.SessionScope(resolve.SessionFolder, "lib")) != 1 {
		t.Error("Clear took another folder's session variables with it")
	}
	if len(sends.SessionScope(resolve.SessionEnv, "staging")) != 1 {
		t.Error("Clear took the environment scope with it")
	}
}

// A panel that says "inherited by 6 requests" without saying which of them
// opted out is telling you something untrue about at least one of them.
func TestFolderOverridesNameTheDescendantsThatOptOut(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}

	var auth, header *FolderOverride
	for i := range doc.Overrides {
		switch doc.Overrides[i].What {
		case "auth":
			auth = &doc.Overrides[i]
		case "Accept":
			header = &doc.Overrides[i]
		}
	}
	if auth == nil {
		t.Fatalf("no auth override; overrides = %+v", doc.Overrides)
	}
	if auth.Path != "orders/fixtures/seed-order.http" || auth.How != "uses none" {
		t.Errorf("auth override = %+v", auth)
	}
	if header == nil {
		t.Fatalf("no header override; overrides = %+v", doc.Overrides)
	}
	if header.Path != "orders/cancel-order.http" || !strings.Contains(header.How, resolve.InheritMarker) {
		t.Errorf("header override = %+v", header)
	}
}

// Hooks run automatically; a module runs only when a hook imports it. The
// panel and the tree both have to be able to say which, so the service says
// which — a reader must not have to know the naming convention.
func TestFolderScriptsDistinguishHooksFromModules(t *testing.T) {
	svc, _, _, _ := newFolderService(t)

	orders, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(orders.Scripts) != 2 {
		t.Fatalf("scripts = %+v, want the two hooks", orders.Scripts)
	}
	// Hooks first, pre before post by name.
	if orders.Scripts[0].Name != "_post.js" || orders.Scripts[0].Hook != "post" {
		t.Errorf("scripts[0] = %+v", orders.Scripts[0])
	}
	if orders.Scripts[1].Name != "_pre.js" || orders.Scripts[1].Hook != "pre" {
		t.Errorf("scripts[1] = %+v", orders.Scripts[1])
	}
	// The design shows a line count beside each hook and its source below.
	if orders.Scripts[1].Lines != 3 {
		t.Errorf("_pre.js lines = %d, want 3", orders.Scripts[1].Lines)
	}
	if !strings.Contains(orders.Scripts[1].Source, "idempotencyKey") {
		t.Errorf("_pre.js source = %q", orders.Scripts[1].Source)
	}

	lib, err := svc.Load("lib", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Scripts) != 2 {
		t.Fatalf("lib scripts = %+v", lib.Scripts)
	}
	for _, script := range lib.Scripts {
		if script.Hook != "" {
			t.Errorf("%s is a module, not a hook: %+v", script.Name, script)
		}
	}
}

// Saving writes through the guard and the whole tree is re-walked, because
// every request below now inherits something different.
func TestFolderSaveWritesThroughTheGuard(t *testing.T) {
	svc, _, collections, root := newFolderService(t)
	target := filepath.Join(root, "orders", "_folder.http")

	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	file := *doc.Settings
	entry := *file.Requests[0]
	entry.Headers = append(entry.Headers, httpfile.Header{Name: "X-Tenant", Value: "acme"})
	file.Requests = []*httpfile.Request{&entry}

	after, err := svc.Save("orders", "", file)
	if err != nil {
		t.Fatal(err)
	}
	if !collections.Guard().Suppressed(target) {
		t.Error("the save did not hold the write guard")
	}

	var found bool
	for _, h := range after.Headers {
		if h.Name == "X-Tenant" && h.Value == "acme" && h.Local {
			found = true
		}
	}
	if !found {
		t.Errorf("headers after save = %+v", after.Headers)
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "X-Tenant: acme") {
		t.Errorf("file =\n%s", data)
	}
}

// A folder file holds settings, not requests (docs/FORMAT.md §2.3). Refusing
// to write a request line beats writing one that will be ignored on the next
// read.
func TestFolderSaveRefusesARequestLine(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatal(err)
	}
	file := *doc.Settings
	entry := *file.Requests[0]
	entry.Method, entry.URL = "GET", "https://example.test/"
	file.Requests = []*httpfile.Request{&entry}

	if _, err := svc.Save("orders", "", file); err == nil {
		t.Error("a request line in a folder file should be refused")
	} else if !strings.Contains(err.Error(), "settings, not requests") {
		t.Errorf("error = %v", err)
	}
}

func TestFolderSaveReadme(t *testing.T) {
	svc, _, collections, root := newFolderService(t)
	target := filepath.Join(root, "orders", "README.md")

	doc, err := svc.SaveReadme("orders", "", "# Orders\n\nRewritten.\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Readme, "Rewritten") {
		t.Errorf("readme = %q", doc.Readme)
	}
	if !collections.Guard().Suppressed(target) {
		t.Error("the readme save did not hold the write guard")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "# Orders\n\nRewritten.\n" {
		t.Errorf("file = %q", data)
	}
}

// A _folder.http that does not parse contributes nothing, and the folder
// still opens — seeing the error is the point (docs/FORMAT.md §3.4).
func TestFolderWithBrokenSettingsStillOpens(t *testing.T) {
	svc, _, _, root := newFolderService(t)
	write(t, filepath.Join(root, "orders", "_folder.http"), "GET nonsense trailing junk here\n")
	if _, err := svc.collections.Tree(); err != nil {
		t.Fatal(err)
	}

	doc, err := svc.Load("orders", "")
	if err != nil {
		t.Fatalf("Load = %v, want the folder to open anyway", err)
	}
	if doc.SettingsError == "" {
		t.Error("no settings error")
	}
	if doc.Settings != nil {
		t.Error("a broken settings file should contribute nothing")
	}
}

func TestFolderLoadRejectsARequest(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	if _, err := svc.Load("orders/create-order.http", ""); err == nil {
		t.Error("Load on a request should fail")
	} else if !strings.Contains(err.Error(), "not a folder") {
		t.Errorf("error = %v", err)
	}
	if _, err := svc.Load("orders/nope", ""); err == nil {
		t.Error("Load on a missing path should fail")
	}
}

// Creating a folder writes a directory *and* a _folder.http inside it, which
// is not decoration: git does not track an empty directory, so a folder
// created without a file in it would vanish on the next clone or checkout.
func TestCreateFolder(t *testing.T) {
	s, _, collections, root := newFolderService(t)

	nodePath, err := s.Create("", "Payment methods")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if nodePath != "payment-methods" {
		t.Errorf("nodePath = %q, want payment-methods", nodePath)
	}

	settings := filepath.Join(root, "payment-methods", collection.FolderFileName)
	info, err := os.Stat(settings)
	if err != nil {
		t.Fatalf("no %s in the new folder, so git would not track it: %v",
			collection.FolderFileName, err)
	}
	if info.Size() == 0 {
		t.Error("the settings file is empty, so git still has nothing to track")
	}

	// It has to parse, and it must declare nothing: a new folder inherits
	// everything from above.
	body := readFile(t, settings)
	file, err := httpfile.ParseString(body)
	if err != nil {
		t.Fatalf("the created %s does not parse: %v\n%s", collection.FolderFileName, err, body)
	}
	entry := file.Requests[0]
	if len(entry.Headers) != 0 || len(entry.Directives) != 0 || len(entry.Variables) != 0 {
		t.Errorf("a new folder should declare nothing, got %d headers, %d directives, %d variables",
			len(entry.Headers), len(entry.Directives), len(entry.Variables))
	}

	// And it is in the tree as a folder.
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find(nodePath)
	if node == nil || node.Kind != collection.KindFolder {
		t.Fatalf("the created folder is not a folder in the tree: %+v", node)
	}
}

func TestCreateFolderResolvesCollisions(t *testing.T) {
	s, _, _, _ := newFolderService(t)
	first, err := s.Create("", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	if first != "reports" || second != "reports-2" {
		t.Errorf("got %q then %q, want reports then reports-2", first, second)
	}
}

// `env` at the root is the environment directory, not a request folder
// (docs/FORMAT.md §2.1, §4.3), so the name is reserved.
func TestCreateFolderWillNotClaimTheEnvDirectory(t *testing.T) {
	s, _, _, _ := newFolderService(t)
	got, err := s.Create("", "env")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got == collection.EnvDirName {
		t.Errorf("Create made a folder called %q, which is the environment directory", got)
	}
}

// Renaming a folder renames its directory: a folder has no `# @name`, its
// name is the directory's name (docs/FORMAT.md §2.1).
func TestRenameFolder(t *testing.T) {
	svc, _, collections, root := newFolderService(t)

	got, err := svc.Rename("orders", "Placements")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got != "placements" {
		t.Fatalf("nodePath = %q, want placements", got)
	}
	if _, err := os.Stat(filepath.Join(root, "orders")); !os.IsNotExist(err) {
		t.Error("the old directory is still there")
	}
	// Everything inside came with it, subfolders included.
	if _, err := os.Stat(filepath.Join(root, "placements", "fixtures", "seed-order.http")); err != nil {
		t.Errorf("the contents did not move: %v", err)
	}
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Find("placements") == nil {
		t.Error("the renamed folder is not in the tree")
	}
}

func TestRenameFolderRefusesTheCollectionRoot(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	if _, err := svc.Rename("", "Anything"); err == nil {
		t.Error("Rename accepted the collection root")
	}
}

// A duplicate is the whole subtree, `.order` files included: the copy holds
// the same entries, so it holds the same arrangement of them.
func TestDuplicateFolderCopiesEverythingInside(t *testing.T) {
	svc, _, _, root := newFolderService(t)
	write(t, filepath.Join(root, "orders", "fixtures", ".order"), "# Mine.\nseed-order.http\n")

	got, err := svc.Duplicate("orders")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if got != "orders-copy" {
		t.Fatalf("nodePath = %q, want orders-copy", got)
	}
	for _, rel := range []string{
		filepath.Join("orders-copy", "_folder.http"),
		filepath.Join("orders-copy", "create-order.http"),
		filepath.Join("orders-copy", "fixtures", "seed-order.http"),
		filepath.Join("orders-copy", "fixtures", ".order"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s did not come with the copy: %v", rel, err)
		}
	}
	// The original is untouched.
	if _, err := os.Stat(filepath.Join(root, "orders", "create-order.http")); err != nil {
		t.Errorf("the original lost a file: %v", err)
	}
}

// Deleting a folder takes everything in it, and takes its line out of the
// parent's `.order`.
func TestDeleteFolderAndItsOrderLine(t *testing.T) {
	svc, _, _, root := newFolderService(t)
	write(t, filepath.Join(root, ".order"), "# Mine.\norders/\nlib/\n")

	if err := svc.Delete("orders"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "orders")); !os.IsNotExist(err) {
		t.Error("the directory is still there")
	}
	got := readFile(t, filepath.Join(root, ".order"))
	want := "# Mine.\nlib/\n"
	if got != want {
		t.Errorf(".order = %q, want %q", got, want)
	}
}

func TestDeleteFolderRefusesTheCollectionRoot(t *testing.T) {
	svc, _, _, _ := newFolderService(t)
	if err := svc.Delete(""); err == nil {
		t.Error("Delete accepted the collection root")
	}
}
