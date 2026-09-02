package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "cfg", "otis", "settings.json"))
}

func TestGetOnMissingFileReturnsZero(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastCollection != "" || len(got.Recents) != 0 || got.Panes.SidebarWidth != 0 {
		t.Fatalf("Get on a missing file = %+v, want the zero value", got)
	}
}

func TestRoundTrip(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	want := Settings{
		Panes:          Panes{SidebarWidth: 260, ResponseWidth: 480, ResponseCollapsed: true},
		Tabs:           Tabs{Open: []string{"orders/create-order.http", "orders"}, Active: "orders"},
		Recents:        []Recent{{Name: filepath.Base(dir), Path: dir, LastOpened: time.Now().UTC().Truncate(time.Second)}},
		LastCollection: dir,
	}
	if err := s.Set(want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Panes != want.Panes {
		t.Errorf("Panes = %+v, want %+v", got.Panes, want.Panes)
	}
	if got.Tabs.Active != "orders" || len(got.Tabs.Open) != 2 || got.Tabs.Open[0] != "orders/create-order.http" {
		t.Errorf("Tabs = %+v", got.Tabs)
	}
	if got.LastCollection != dir {
		t.Errorf("LastCollection = %q, want %q", got.LastCollection, dir)
	}
	if len(got.Recents) != 1 || !got.Recents[0].LastOpened.Equal(want.Recents[0].LastOpened) {
		t.Errorf("Recents = %+v, want %+v", got.Recents, want.Recents)
	}
	if got.Recents[0].Missing {
		t.Error("an existing directory was marked missing")
	}
}

func TestMissingIsComputedNotPersisted(t *testing.T) {
	s := newTestStore(t)
	gone := filepath.Join(t.TempDir(), "deleted")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(Settings{Recents: []Recent{{Name: "deleted", Path: gone}}}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// On disk the flag is always false...
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"missing": false`) {
		t.Errorf("missing was not written as false:\n%s", raw)
	}
	// ...and true only once the directory has actually gone.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get()
	if !got.Recents[0].Missing {
		t.Error("a deleted directory was not marked missing")
	}
	// A file where a directory used to be is missing too.
	if err := os.WriteFile(gone, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get()
	if !got.Recents[0].Missing {
		t.Error("a plain file was not marked missing")
	}
}

func TestCorruptFileFallsBackToDefaults(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get()
	if err != nil {
		t.Fatalf("Get on a corrupt file returned an error: %v", err)
	}
	if got.LastCollection != "" {
		t.Fatalf("Get = %+v, want the zero value", got)
	}
	// The next write repairs the file.
	if err := s.Set(Settings{LastCollection: "/tmp/x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, _ = s.Get(); got.LastCollection != "/tmp/x" {
		t.Fatalf("LastCollection = %q after repair", got.LastCollection)
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Set(Settings{LastCollection: "/x"}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("config dir holds %v, want only settings.json", names)
	}
}

func TestUpdateIsReadModifyWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set(Settings{LastCollection: "/a", Panes: Panes{SidebarWidth: 300}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Update(func(v *Settings) { v.LastCollection = "/b" })
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.LastCollection != "/b" || got.Panes.SidebarWidth != 300 {
		t.Fatalf("Update = %+v, want /b with the pane width preserved", got)
	}
	reread, _ := s.Get()
	if reread.LastCollection != "/b" || reread.Panes.SidebarWidth != 300 {
		t.Fatalf("reread = %+v", reread)
	}
}

func TestAddRecentOrdersAndCaps(t *testing.T) {
	now := time.Now()
	var v Settings
	v.AddRecent("a", "/a", now)
	v.AddRecent("b", "/b", now)
	v.AddRecent("a", "/a", now.Add(time.Minute)) // re-open moves to the front
	if len(v.Recents) != 2 || v.Recents[0].Path != "/a" || v.Recents[1].Path != "/b" {
		t.Fatalf("Recents = %+v", v.Recents)
	}
	if v.Recents[0].Name != "a" {
		t.Errorf("Name = %q, want the base name", v.Recents[0].Name)
	}
	for i := 0; i < MaxRecents+5; i++ {
		v.AddRecent("x", filepath.Join("/x", string(rune('a'+i))), now)
	}
	if len(v.Recents) != MaxRecents {
		t.Fatalf("len(Recents) = %d, want %d", len(v.Recents), MaxRecents)
	}
}

func TestRemoveRecent(t *testing.T) {
	var v Settings
	v.AddRecent("a", "/a", time.Now())
	v.AddRecent("b", "/b", time.Now())
	if !v.RemoveRecent("/a") {
		t.Error("RemoveRecent(/a) = false, want true")
	}
	if v.RemoveRecent("/nope") {
		t.Error("RemoveRecent of an absent path = true, want false")
	}
	if len(v.Recents) != 1 || v.Recents[0].Path != "/b" {
		t.Fatalf("Recents = %+v", v.Recents)
	}
}

func TestNormaliseCapsRecentsOnWrite(t *testing.T) {
	s := newTestStore(t)
	var v Settings
	for i := 0; i < MaxRecents+3; i++ {
		v.Recents = append(v.Recents, Recent{Path: filepath.Join("/x", string(rune('a'+i)))})
	}
	if err := s.Set(v); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get()
	if len(got.Recents) != MaxRecents {
		t.Fatalf("len(Recents) = %d, want %d", len(got.Recents), MaxRecents)
	}
}
