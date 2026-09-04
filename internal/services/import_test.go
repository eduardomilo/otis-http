package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/settings"
)

// A small but real Postman v2.1 export: two requests in a folder, so the
// counts and the tree it produces are worth asserting on.
const postmanExport = `{
  "info": { "name": "Acme API", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" },
  "item": [
    {
      "name": "Orders",
      "item": [
        { "name": "Create order",
          "request": { "method": "POST", "url": { "raw": "https://api.acme.com/v2/orders" },
            "header": [{ "key": "Content-Type", "value": "application/json" }],
            "body": { "mode": "raw", "raw": "{\"sku\":\"A-1\"}" } } },
        { "name": "List orders",
          "request": { "method": "GET", "url": { "raw": "https://api.acme.com/v2/orders" } } }
      ]
    }
  ]
}`

func importFixture(t *testing.T) (*ImportService, *CollectionService, string) {
	t.Helper()
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	// The dialogs are never reached: these tests drive PlanFile and Retarget
	// directly, because a native picker is not something a test can answer.
	svc := NewImportService(collections, nil)

	dir := t.TempDir()
	export := filepath.Join(dir, "acme.postman_collection.json")
	write(t, export, postmanExport)
	return svc, collections, export
}

// With nothing open, an import becomes a new collection beside the export —
// that is how you *get* a collection, which is what the start screen is for.
func TestAnImportWithNothingOpenLandsBesideTheExport(t *testing.T) {
	svc, collections, export := importFixture(t)

	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CollectionName != "Acme API" {
		t.Errorf("collection name = %q", plan.CollectionName)
	}
	if plan.Requests != 2 || plan.Folders != 1 {
		t.Errorf("got %d requests and %d folders, want 2 and 1", plan.Requests, plan.Folders)
	}
	if want := filepath.Join(filepath.Dir(export), "acme-api"); plan.Destination != want {
		t.Errorf("destination = %q, want %q", plan.Destination, want)
	}
	if plan.Inside {
		t.Error("it is marked as landing inside a collection, with none open")
	}
	if plan.Blocked != "" {
		t.Errorf("blocked: %s", plan.Blocked)
	}
	// Nothing is on disk yet: the whole point of planning first.
	if _, err := os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Error("planning wrote something")
	}

	result, err := svc.Commit(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Root != plan.Destination {
		t.Errorf("root = %q, want %q", result.Root, plan.Destination)
	}
	// It became the open collection.
	if collections.Current().Path != plan.Destination {
		t.Errorf("the import did not open: %q", collections.Current().Path)
	}
	for _, rel := range []string{"orders/create-order.http", "orders/list-orders.http"} {
		if _, err := os.Stat(filepath.Join(plan.Destination, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was not written: %v", rel, err)
		}
	}
}

// With a collection open, an import goes into it as a new folder, so a
// Postman export can be pulled into a collection you already have.
func TestAnImportWithACollectionOpenLandsInsideIt(t *testing.T) {
	svc, collections, export := importFixture(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "existing.http"), "GET http://127.0.0.1:1/x\n")
	write(t, filepath.Join(root, ".order"), "# hand written\nexisting\n")
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	orderBefore, err := os.ReadFile(filepath.Join(root, ".order"))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Inside {
		t.Fatalf("destination %q is not marked as inside the open collection", plan.Destination)
	}
	if plan.NodePath != "acme-api" {
		t.Errorf("node path = %q, want acme-api", plan.NodePath)
	}
	if want := filepath.Join(root, "acme-api"); plan.Destination != want {
		t.Errorf("destination = %q, want %q", plan.Destination, want)
	}

	result, err := svc.Commit(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.NodePath != "acme-api" {
		t.Errorf("result node path = %q", result.NodePath)
	}
	// The collection did not change out from under the person.
	if collections.Current().Path != root {
		t.Errorf("the open collection changed to %q", collections.Current().Path)
	}
	// The imported requests are in the tree.
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Find("acme-api/orders/create-order.http") == nil {
		t.Error("the imported request is not in the tree")
	}
	if loaded.Find("existing.http") == nil {
		t.Error("the existing request disappeared")
	}

	// The invariant that has its own test elsewhere, holding for an import
	// into an open collection: `.order` is never rewritten except by an
	// explicit reorder (docs/FORMAT.md §2.2). The new folder is unlisted, so
	// it sorts alphabetically, and that is the whole mechanism.
	orderAfter, err := os.ReadFile(filepath.Join(root, ".order"))
	if err != nil {
		t.Fatal(err)
	}
	if string(orderAfter) != string(orderBefore) {
		t.Errorf("the parent's .order was rewritten:\n%q\n%q", orderBefore, orderAfter)
	}
	// The import's *own* directory is fresh, so it gets one.
	if _, err := os.Stat(filepath.Join(root, "acme-api", ".order")); err != nil {
		t.Errorf("the imported folder has no .order: %v", err)
	}
}

// A destination with files in it is refused before the button is pressed,
// rather than after. Overwriting is not offered: the CLI's --force is for
// somebody who typed a path.
func TestADestinationWithFilesInItIsBlocked(t *testing.T) {
	svc, _, export := importFixture(t)
	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}

	occupied := t.TempDir()
	write(t, filepath.Join(occupied, "somebody-elses.http"), "GET http://127.0.0.1:1/x\n")
	plan, err = svc.Retarget(plan.ID, occupied)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked == "" {
		t.Fatal("a directory with files in it was not blocked")
	}
	if !strings.Contains(plan.Blocked, "somebody-elses.http") {
		t.Errorf("the reason does not name what is in the way: %s", plan.Blocked)
	}
	// And committing anyway is refused, so the button being disabled is a
	// courtesy on top rather than the safety.
	if _, err := svc.Commit(plan.ID); err == nil {
		t.Fatal("a blocked plan committed")
	}
	if _, err := os.Stat(filepath.Join(occupied, "orders")); !os.IsNotExist(err) {
		t.Error("it wrote into the occupied directory")
	}
	// The file that was there is untouched.
	if _, err := os.Stat(filepath.Join(occupied, "somebody-elses.http")); err != nil {
		t.Error("the existing file was disturbed")
	}
}

// An empty directory is fine: only *content* is in the way.
func TestAnEmptyDestinationIsAllowed(t *testing.T) {
	svc, _, export := importFixture(t)
	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	plan, err = svc.Retarget(plan.ID, empty)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocked != "" {
		t.Fatalf("an empty directory was blocked: %s", plan.Blocked)
	}
	if _, err := svc.Commit(plan.ID); err != nil {
		t.Fatal(err)
	}
}

// A file that is not a Postman export fails with the importer's own message,
// and holds no plan afterwards.
func TestANonPostmanFileIsARefusal(t *testing.T) {
	svc, _, _ := importFixture(t)
	bad := filepath.Join(t.TempDir(), "notes.json")
	write(t, bad, `{"hello":"world"}`)

	if _, err := svc.PlanFile(bad); err == nil {
		t.Fatal("a file that is not a collection planned successfully")
	}
	svc.mu.Lock()
	held := len(svc.plans)
	svc.mu.Unlock()
	if held != 0 {
		t.Errorf("%d plans are held after a failure", held)
	}
}

// A cancelled import does not keep a whole converted collection in memory.
func TestDiscardingAPlanFreesIt(t *testing.T) {
	svc, _, export := importFixture(t)
	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}
	svc.Discard(plan.ID)
	if _, err := svc.Commit(plan.ID); err == nil {
		t.Fatal("a discarded plan committed")
	}
	svc.mu.Lock()
	held := len(svc.plans)
	svc.mu.Unlock()
	if held != 0 {
		t.Errorf("%d plans are still held", held)
	}
}

// Committing spends the plan, so a double-click cannot write twice.
func TestAPlanIsSpentOnCommit(t *testing.T) {
	svc, _, export := importFixture(t)
	plan, err := svc.PlanFile(export)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commit(plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commit(plan.ID); err == nil {
		t.Fatal("the same plan committed twice")
	}
}
