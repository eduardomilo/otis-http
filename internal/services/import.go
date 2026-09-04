package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/importer/postman"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ImportService turns a Postman export into an Otis collection.
//
// The conversion itself is `internal/importer/postman`, which the CLI already
// uses; this is the window's half of it. What the window adds is the thing a
// flag cannot: it *plans* first and shows what would be written, including
// what the importer had to skip, and only writes when a person says so
// (DESIGN-NOTES §8.2 — every write to disk is announced before it happens).
// The importer separates Plan from Write precisely so a caller can do that.
//
// An import lands in one of two places, and which one is decided by whether a
// collection is open:
//
//   - **Nothing open** — a new folder beside the export, named for the
//     collection. That is how you *get* a collection, which is what the start
//     screen's card is for.
//   - **A collection open** — a folder inside it, so a Postman export can be
//     pulled into a collection you already have. It goes in as a new
//     subfolder and touches nothing else: `.order` is written into the new
//     directory (fresh, so there is nothing to preserve — docs/FORMAT.md
//     §2.2) and the parent's is left alone, so the new folder sorts
//     alphabetically like anything else added.
type ImportService struct {
	collections *CollectionService
	dialogs     *DialogService
	app         *application.App

	mu sync.Mutex
	// plans holds what has been planned but not yet written. A plan is the
	// whole converted collection in memory, so it is dropped as soon as it
	// is written or abandoned.
	plans map[string]*heldPlan
}

// heldPlan is a planned import waiting for an answer.
//
// It carries the destination as well as the converted files, because the
// destination is the part a person changes between planning and committing —
// and the window is not trusted to hand it back, since the window is where a
// stale copy would come from.
type heldPlan struct {
	out *postman.Output
	// source is the export's path, kept so a re-proposal after a change of
	// mind lands beside the same file.
	source string
	// destination is absolute, and is re-checked against the disk every time
	// the plan is read: a directory can gain files while a dialog is open.
	destination string
}

// NewImportService constructs the service.
func NewImportService(collections *CollectionService, dialogs *DialogService) *ImportService {
	return &ImportService{collections: collections, dialogs: dialogs, plans: map[string]*heldPlan{}}
}

// ServiceStartup resolves the application.
func (s *ImportService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// ImportNote is one line of the importer's report.
type ImportNote struct {
	// Path is the file it concerns, relative to the import, or "" for the
	// collection as a whole.
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// ImportPlan is what an import would do, before it does any of it.
type ImportPlan struct {
	// ID identifies the held plan for Commit.
	ID string `json:"id"`
	// Source is the export's path, for the dialog to name what it read.
	Source string `json:"source"`
	// CollectionName is the name Postman gave it.
	CollectionName string `json:"collectionName"`

	Requests     int `json:"requests"`
	Folders      int `json:"folders"`
	Environments int `json:"environments"`
	// Files is how many files would be written.
	Files int `json:"files"`

	// Destination is the absolute path that would receive it.
	Destination string `json:"destination"`
	// Inside is true when Destination is within the open collection, which
	// changes the wording and means the tree will refresh rather than the
	// collection being replaced.
	Inside bool `json:"inside"`
	// NodePath is the destination relative to the open collection's root,
	// set only when Inside.
	NodePath string `json:"nodePath,omitempty"`

	// Blocked explains why this destination cannot be written, or is empty.
	// The window shows it and keeps the Import button disabled; the fix is
	// always to choose somewhere else, because overwriting a directory full
	// of somebody's files is not something to offer behind a button.
	Blocked string `json:"blocked,omitempty"`

	Skipped  []ImportNote `json:"skipped"`
	Warnings []ImportNote `json:"warnings"`
	Notes    []ImportNote `json:"notes"`
}

// ImportResult is where an import ended up.
type ImportResult struct {
	// Root is the collection that is now open.
	Root string `json:"root"`
	// NodePath is the imported folder within it, "" when the import became
	// the collection itself.
	NodePath string `json:"nodePath,omitempty"`
	Files    int    `json:"files"`
}

// Choose opens the file picker and plans what the chosen export would become.
//
// Nothing is written. Cancelling returns a zero plan with no error.
func (s *ImportService) Choose() (ImportPlan, error) {
	path, err := s.dialogs.OpenPostmanExport()
	if err != nil || path == "" {
		return ImportPlan{}, err
	}
	return s.PlanFile(path)
}

// PlanFile plans an export at a known path, without writing.
func (s *ImportService) PlanFile(path string) (ImportPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportPlan{}, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	out, err := postman.Plan(data)
	if err != nil {
		// The importer's errors already name what is wrong with the file.
		return ImportPlan{}, err
	}

	id, err := planID()
	if err != nil {
		return ImportPlan{}, err
	}
	s.mu.Lock()
	s.plans[id] = &heldPlan{out: out, source: path}
	s.mu.Unlock()

	plan := ImportPlan{
		ID:             id,
		Source:         path,
		CollectionName: out.Report.CollectionName,
		Requests:       out.Report.Requests,
		Folders:        out.Report.Folders,
		Environments:   out.Report.Environments,
		Files:          len(out.Report.Files),
		Skipped:        notesOf(out.Report.Skipped),
		Warnings:       notesOf(out.Report.Warnings),
		Notes:          notesOf(out.Report.Notes),
	}
	s.propose(&plan, path)
	s.remember(id, plan.Destination)
	return plan, nil
}

// Retarget points a held plan at a directory the person chose.
func (s *ImportService) Retarget(id, dir string) (ImportPlan, error) {
	plan, err := s.held(id)
	if err != nil {
		return ImportPlan{}, err
	}
	if dir == "" {
		return plan, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ImportPlan{}, err
	}
	plan.Destination = abs
	s.locate(&plan)
	s.check(&plan)
	s.remember(id, abs)
	return plan, nil
}

// ChooseDestination opens the directory picker for a held plan.
func (s *ImportService) ChooseDestination(id string) (ImportPlan, error) {
	dir, err := s.dialogs.OpenImportDestination()
	if err != nil || dir == "" {
		return s.held(id)
	}
	return s.Retarget(id, dir)
}

// Commit writes a held plan and opens or refreshes the collection.
func (s *ImportService) Commit(id string) (ImportResult, error) {
	plan, err := s.held(id)
	if err != nil {
		return ImportResult{}, err
	}
	if plan.Blocked != "" {
		return ImportResult{}, fmt.Errorf("%s", plan.Blocked)
	}
	s.mu.Lock()
	held := s.plans[id]
	s.mu.Unlock()
	if held == nil {
		return ImportResult{}, fmt.Errorf("that import is no longer available; choose the file again")
	}
	out := held.out

	if plan.Inside {
		// Writing into the open collection, so the watcher must not report
		// it as somebody else's change (CLAUDE.md). Every write to a
		// collection goes inside the guard.
		release := s.collections.Guard().Writing(plan.Destination)
		err = postman.Write(out, plan.Destination, false)
		release()
		if err != nil {
			return ImportResult{}, err
		}
		s.forget(id)
		// The guard suppressed the watcher, so the save announces itself.
		if err := s.collections.Refresh(); err != nil {
			return ImportResult{}, err
		}
		return ImportResult{
			Root: s.collections.Current().Path, NodePath: plan.NodePath, Files: plan.Files,
		}, nil
	}

	if err := postman.Write(out, plan.Destination, false); err != nil {
		return ImportResult{}, err
	}
	s.forget(id)
	if _, err := s.collections.Open(plan.Destination); err != nil {
		return ImportResult{}, err
	}
	// Open returns the tree to its *caller* and emits only
	// events.CollectionOpened, which carries the collection and not the
	// tree — that is enough for the window when the window asked, and not
	// enough here, because Go asked. Refresh is the mechanism a write Otis
	// makes uses to announce itself (CLAUDE.md), and without it the sidebar
	// stays empty until something else re-walks.
	if err := s.collections.Refresh(); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Root: plan.Destination, Files: plan.Files}, nil
}

// Discard drops a plan the person abandoned, so a cancelled import does not
// keep a whole converted collection in memory.
func (s *ImportService) Discard(id string) {
	s.forget(id)
}

// --- internals ----------------------------------------------------------

// propose picks the destination an import is offered by default.
func (s *ImportService) propose(plan *ImportPlan, source string) {
	name := collection.Slug(plan.CollectionName)
	if name == "" {
		// A collection Postman never named still has to land somewhere it
		// can be told apart from the export beside it.
		name = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
		if name = collection.Slug(name); name == "" {
			name = "imported"
		}
	}

	if root := s.collections.Current().Path; root != "" {
		// A collection is open, so the import goes into it as a new folder.
		plan.Destination = filepath.Join(root, name)
	} else {
		// Nothing open: beside the export, which is the only directory this
		// person has pointed at.
		plan.Destination = filepath.Join(filepath.Dir(source), name)
	}
	s.locate(plan)
	s.check(plan)
}

// locate works out whether a destination is inside the open collection.
func (s *ImportService) locate(plan *ImportPlan) {
	plan.Inside, plan.NodePath = false, ""
	root := s.collections.Current().Path
	if root == "" {
		return
	}
	rel, err := filepath.Rel(root, plan.Destination)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	plan.Inside = true
	plan.NodePath = filepath.ToSlash(rel)
}

// check reports why a destination cannot be written to, if it cannot.
//
// It mirrors postman.Write's own refusal rather than trusting it, so the
// window can say so *before* the button is pressed. Overwriting is not
// offered: the CLI's --force exists for a person who typed a path, and a
// button that silently merges an import into a directory of somebody else's
// files is not the same thing.
func (s *ImportService) check(plan *ImportPlan) {
	plan.Blocked = ""
	entries, err := os.ReadDir(plan.Destination)
	if err != nil {
		if os.IsNotExist(err) {
			return // It will be created.
		}
		plan.Blocked = err.Error()
		return
	}
	var visible []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") || e.Name() == ".order" {
			visible = append(visible, e.Name())
		}
	}
	if len(visible) > 0 {
		plan.Blocked = fmt.Sprintf("%s already has files in it (%s). Choose a folder that does not exist yet, or an empty one.",
			filepath.Base(plan.Destination), strings.Join(visible[:min(len(visible), 3)], ", "))
	}
}

func (s *ImportService) held(id string) (ImportPlan, error) {
	s.mu.Lock()
	held := s.plans[id]
	s.mu.Unlock()
	if held == nil {
		return ImportPlan{}, fmt.Errorf("that import is no longer available; choose the file again")
	}
	out := held.out
	// Rebuilt from Go's copy rather than from anything the window sent back,
	// and re-checked against the disk as it is *now*: a directory can gain
	// files while a dialog is open, and the window's copy of "not blocked"
	// would be the stale one.
	plan := ImportPlan{
		ID: id, Source: held.source, CollectionName: out.Report.CollectionName,
		Requests: out.Report.Requests, Folders: out.Report.Folders,
		Environments: out.Report.Environments, Files: len(out.Report.Files),
		Destination: held.destination,
		Skipped:     notesOf(out.Report.Skipped), Warnings: notesOf(out.Report.Warnings),
		Notes: notesOf(out.Report.Notes),
	}
	s.locate(&plan)
	s.check(&plan)
	return plan, nil
}

// remember stores the destination a plan is currently pointed at.
func (s *ImportService) remember(id, destination string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if held := s.plans[id]; held != nil {
		held.destination = destination
	}
}

func (s *ImportService) forget(id string) {
	s.mu.Lock()
	delete(s.plans, id)
	s.mu.Unlock()
}

func notesOf(entries []postman.Entry) []ImportNote {
	out := make([]ImportNote, 0, len(entries))
	for _, e := range entries {
		out = append(out, ImportNote{Path: e.Path, Detail: e.Msg})
	}
	return out
}

func planID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("services: minting an import id: %w", err)
	}
	return "i_" + hex.EncodeToString(buf), nil
}
