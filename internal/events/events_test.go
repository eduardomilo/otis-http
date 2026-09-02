package events

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The registry and the constants must not drift apart.
func TestRegistryCoversEveryConstant(t *testing.T) {
	want := map[string]string{
		"AppReady":            AppReady,
		"CollectionOpened":    CollectionOpened,
		"CollectionChanged":   CollectionChanged,
		"GitChanged":          GitChanged,
		"SettingsChanged":     SettingsChanged,
		"SendStarted":         SendStarted,
		"SendComplete":        SendComplete,
		"SendError":           SendError,
		"SessionVarsChanged":  SessionVarsChanged,
		"EnvironmentsChanged": EnvironmentsChanged,
		"RunStarted":          RunStarted,
		"RunResult":           RunResult,
		"RunComplete":         RunComplete,
	}
	if len(Registry) != len(want) {
		t.Fatalf("Registry has %d entries, want %d", len(Registry), len(want))
	}
	for _, e := range Registry {
		v, ok := want[e.Ident]
		if !ok {
			t.Errorf("Registry has unknown entry %q", e.Ident)
			continue
		}
		if v != e.Name {
			t.Errorf("%s: registry name %q, constant %q", e.Ident, e.Name, v)
		}
		if e.Doc == "" {
			t.Errorf("%s: missing doc", e.Ident)
		}
	}
}

// The generated TypeScript mirror is committed, so it must match the Go side.
// If this fails, run: go generate ./internal/events
func TestTypeScriptMirrorIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "lib", "events.gen.ts")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if !bytes.Equal(got, TypeScript()) {
		t.Errorf("%s is stale; run: go generate ./internal/events", path)
	}
}
