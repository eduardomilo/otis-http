package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// KeychainState is what the window may say about the machine's credential
// store.
//
// Unavailable is a normal state, not an error (secrets.ErrUnavailable): a
// collection whose environments reference secrets is still perfectly readable,
// because the references are the part that is committed. The editor then shows
// the references and says the values cannot be reached, rather than refusing.
type KeychainState struct {
	Available bool `json:"available"`
	// Reason explains an unavailable keychain, in words a person can act on.
	// It never names a key's value, because there is none to name.
	Reason string `json:"reason,omitempty"`
}

// EnvironmentSummary is one row of the environment list (screen 1c's sidebar).
type EnvironmentSummary struct {
	Name string `json:"name"`
	// Path is relative to the collection root, e.g. "env/staging.json".
	Path      string `json:"path"`
	Variables int    `json:"variables"`
	Secrets   int    `json:"secrets"`
	Active    bool   `json:"active"`
	// ConfirmBeforeSend is the environment's own "$otis" setting
	// (docs/FORMAT.md §4.3). It is what paints the row's dot as production.
	ConfirmBeforeSend bool   `json:"confirmBeforeSend"`
	Description       string `json:"description,omitempty"`
	// ReferencedBy is how many requests in the collection mention at least
	// one of this environment's variables. See referenceCounts.
	ReferencedBy int `json:"referencedBy"`
	// Error is the parse error for a file that could not be read. The row
	// still appears: an environment you cannot parse is exactly the one you
	// need to see in order to fix it.
	Error string `json:"error,omitempty"`
}

// Environments is the whole environment surface in one payload: the list, the
// active one, and whether this machine can reach a keychain at all.
type Environments struct {
	Active   string               `json:"active"`
	Items    []EnvironmentSummary `json:"items"`
	Keychain KeychainState        `json:"keychain"`
}

// EnvironmentRow is one variable in the editor's table (screen 1c).
type EnvironmentRow struct {
	Name string `json:"name"`
	// Value is the literal value — and is **empty for a secret**, always.
	//
	// Not a mask: a mask is a string, and a string that sometimes holds a
	// value and sometimes holds dots is one refactor away from shipping the
	// value. The window is told Secret is true and draws the dots itself
	// (DESIGN-NOTES §8.3), so there is no path by which a resolved secret
	// value can reach it.
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	// Present reports that a secret reference has a value in this machine's
	// keychain. False means a teammate committed the reference and this
	// machine has never been given the value — which the editor has to be
	// able to say, because it is the difference between "ready" and "this
	// request will fail".
	Present bool `json:"present"`
	// Key is the keychain key, <collection>/<env>/<name>. It is public: the
	// design puts it on screen (screen 1c), and every part of it is already
	// committed.
	Key string `json:"key"`
	// Kind is the JSON shape the value is written as: a number stays a
	// number in the file.
	Kind resolve.EnvKind `json:"kind"`
}

// EnvironmentDocument is one environment open in the editor.
type EnvironmentDocument struct {
	Name              string           `json:"name"`
	Path              string           `json:"path"`
	Active            bool             `json:"active"`
	ConfirmBeforeSend bool             `json:"confirmBeforeSend"`
	Description       string           `json:"description,omitempty"`
	Rows              []EnvironmentRow `json:"rows"`
	Variables         int              `json:"variables"`
	Secrets           int              `json:"secrets"`
	// ReferencedBy is how many requests in the collection mention at least
	// one of this environment's variables (screen 1c's status bar). See
	// resolve.ReferencedNames for what "mention" means and why it is not the
	// same as "resolve from here".
	ReferencedBy int           `json:"referencedBy"`
	Keychain     KeychainState `json:"keychain"`
}

// EnvironmentService is the environment editor's service (screen 1c): it
// lists, reads and writes env/*.json, and it is the only thing that puts a
// secret into the machine's keychain or takes one out.
//
// # The one rule
//
// A resolved secret value never crosses this boundary. Every method here
// either takes a value the user just typed (window → Go, which is how a value
// gets in) or reports *about* a secret without its value (Go → window). There
// is deliberately no method that returns one. The design's Reveal control is
// served by CopySecretValue, which puts the value on the system clipboard from
// Go: the user gets at their own credential, and the webview never holds it.
type EnvironmentService struct {
	app         *application.App
	collections *CollectionService
	settings    *settings.Store
	// store is the real secret store — the only one in the process that can
	// return a value. Read paths elsewhere use secrets.Placeholder.
	store secrets.Store
}

// NewEnvironmentService constructs the service. store must be the process's
// real secret store.
func NewEnvironmentService(collections *CollectionService, store *settings.Store, secretStore secrets.Store) *EnvironmentService {
	if secretStore == nil {
		secretStore = secrets.NewMemory()
	}
	return &EnvironmentService{collections: collections, settings: store, store: secretStore}
}

// ServiceStartup resolves the application and asks to be told when the
// collection changed on disk, so an environment file edited in another editor
// updates the window the same way a request file does.
func (s *EnvironmentService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	s.collections.OnDiskChange(func() {
		if list, err := s.List(); err == nil {
			s.emit(events.EnvironmentsChanged, list)
		}
	})
	return nil
}

// List returns every environment in the collection, with the active one
// marked. No collection open, or no env/ directory, is an empty list rather
// than an error: a collection need not have environments at all.
func (s *EnvironmentService) List() (Environments, error) {
	list := Environments{Keychain: s.keychain()}
	root := s.collections.Current().Path
	if root == "" {
		return list, nil
	}
	active, err := s.active()
	if err != nil {
		return list, err
	}
	list.Active = active

	names, err := resolve.ListEnvironments(root)
	if err != nil {
		return list, fmt.Errorf("listing the environments in %s: %w", collection.EnvDirName, err)
	}
	loaded := make([]*resolve.Environment, len(names))
	for i, name := range names {
		item := EnvironmentSummary{
			Name:   name,
			Path:   resolve.EnvPath(name),
			Active: name == active,
		}
		env, err := resolve.LoadEnvironment(root, name)
		if err != nil {
			item.Error = err.Error()
		} else {
			loaded[i] = env
			item.Variables = len(env.Values)
			item.Secrets = len(env.SecretNames())
			item.ConfirmBeforeSend = env.Meta.ConfirmBeforeSend
			item.Description = env.Meta.Description
		}
		list.Items = append(list.Items, item)
	}
	for i, count := range s.referenceCounts(loaded) {
		list.Items[i].ReferencedBy = count
	}
	return list, nil
}

// Load reads one environment for the editor.
func (s *EnvironmentService) Load(name string) (EnvironmentDocument, error) {
	root, env, err := s.read(name)
	if err != nil {
		return EnvironmentDocument{}, err
	}
	active, err := s.active()
	if err != nil {
		return EnvironmentDocument{}, err
	}
	doc := EnvironmentDocument{
		Name:              env.Name,
		Path:              env.Path,
		Active:            env.Name == active,
		ConfirmBeforeSend: env.Meta.ConfirmBeforeSend,
		Description:       env.Meta.Description,
		Keychain:          s.keychain(),
		Variables:         len(env.Values),
		Secrets:           len(env.SecretNames()),
		ReferencedBy:      s.referencedBy(env),
	}
	key := collection.DisplayName(root)
	for _, varName := range env.OrderedNames() {
		value := env.Values[varName]
		row := EnvironmentRow{Name: varName, Secret: value.Secret, Kind: value.Kind}
		if value.Secret {
			row.Key = secrets.Key(key, env.Name, varName)
			row.Present = s.present(row.Key)
		} else {
			row.Value = value.Value
		}
		doc.Rows = append(doc.Rows, row)
	}
	return doc, nil
}

// referenceCounts returns, for each environment, how many requests in the
// collection mention at least one of its variables. A nil entry counts 0.
//
// One walk for every environment, not one per environment: the expensive part
// is resolve.ReferencedNames, which computes a request's inheritance, and that
// answer is the same whichever environment is being counted. It walks the
// cached tree rather than re-reading anything, and it resolves nothing — no
// value is looked up and no secret is touched.
func (s *EnvironmentService) referenceCounts(envs []*resolve.Environment) []int {
	counts := make([]int, len(envs))
	wanted := false
	for _, env := range envs {
		if env != nil && len(env.Values) > 0 {
			wanted = true
		}
	}
	if !wanted {
		return counts
	}
	loaded, err := s.collections.Loaded()
	if err != nil {
		return counts
	}
	loaded.Walk(func(node *collection.Node) bool {
		if node.Kind != collection.KindRequest || node.Request == nil {
			return true
		}
		names := resolve.ReferencedNames(node)
		for i, env := range envs {
			if env == nil {
				continue
			}
			for _, name := range names {
				if _, ok := env.Values[name]; ok {
					counts[i]++
					break
				}
			}
		}
		return true
	})
	return counts
}

// referencedBy is referenceCounts for one environment.
func (s *EnvironmentService) referencedBy(env *resolve.Environment) int {
	return s.referenceCounts([]*resolve.Environment{env})[0]
}

// Activate makes name the active environment, or deactivates every one when
// name is "". Only the name is persisted (settings.ActiveEnv).
func (s *EnvironmentService) Activate(name string) (Environments, error) {
	if name != "" {
		// Refuse a name that does not resolve, so the window cannot end up
		// resolving every request against an environment that is not there.
		if _, _, err := s.read(name); err != nil {
			return Environments{}, err
		}
	}
	if _, err := s.settings.Update(func(v *settings.Settings) { v.ActiveEnv = name }); err != nil {
		return Environments{}, fmt.Errorf("recording the active environment: %w", err)
	}
	s.emit(events.SettingsChanged, nil)
	return s.announce()
}

// Create writes an empty env/<name>.json and returns it.
func (s *EnvironmentService) Create(name string) (EnvironmentDocument, error) {
	root, err := s.root()
	if err != nil {
		return EnvironmentDocument{}, err
	}
	if err := validEnvName(name); err != nil {
		return EnvironmentDocument{}, err
	}
	target, err := s.file(root, name)
	if err != nil {
		return EnvironmentDocument{}, err
	}
	if _, err := os.Stat(target); err == nil {
		return EnvironmentDocument{}, fmt.Errorf("the environment %q already exists", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return EnvironmentDocument{}, fmt.Errorf("creating %s: %w", collection.EnvDirName, err)
	}
	empty := &resolve.Environment{Name: name, Path: resolve.EnvPath(name), Values: map[string]resolve.EnvValue{}}
	if err := s.writeFile(target, empty); err != nil {
		return EnvironmentDocument{}, err
	}
	return s.Load(name)
}

// Duplicate copies env/<name>.json to a new environment and returns it.
//
// The copy carries the original's secret *references*, and its stored values
// come with them: a reference is keyed <collection>/<env>/<name>, so a copy
// whose keys nobody wrote would be an environment full of references to
// nothing, which is not what duplicating "staging" means. The values move
// inside Go and across the keychain — no value is returned, logged or named,
// and the count of them is all the window is told.
//
// A value that cannot be read is skipped rather than failing the whole copy:
// the reference is still copied, and the environment editor shows it as a
// secret with no value set, which is the same state a reference somebody
// pulled from a colleague's branch is in.
//
// The name is "<name>-copy", then "<name>-copy-2", and so on.
func (s *EnvironmentService) Duplicate(name string) (EnvironmentDocument, error) {
	root, env, err := s.read(name)
	if err != nil {
		return EnvironmentDocument{}, err
	}
	list, err := s.List()
	if err != nil {
		return EnvironmentDocument{}, err
	}
	used := make(map[string]bool, len(list.Items))
	for _, summary := range list.Items {
		used[summary.Name] = true
	}

	// Hyphenated rather than "staging copy": an environment's name *is* its
	// file name (docs/FORMAT.md §4.3), and it is also what `otis run --env`
	// takes, so a space would put a quoted argument in everybody's CI script.
	copyOf := name + "-copy"
	for i := 2; used[copyOf]; i++ {
		copyOf = fmt.Sprintf("%s-copy-%d", name, i)
	}
	target, err := s.file(root, copyOf)
	if err != nil {
		return EnvironmentDocument{}, err
	}

	duplicate := &resolve.Environment{
		Name:   copyOf,
		Path:   resolve.EnvPath(copyOf),
		Values: make(map[string]resolve.EnvValue, len(env.Values)),
		Order:  append([]string(nil), env.Order...),
		Meta:   env.Meta,
	}
	for key, value := range env.Values {
		duplicate.Values[key] = value
	}

	// The keychain first: a file whose references resolve is better than a
	// file that appeared before its values did, and a failed write leaves
	// nothing behind to explain. The collection half of the key is the
	// collection's display name, which is what every other caller uses.
	where := collection.DisplayName(root)
	for _, key := range env.Order {
		if !env.Values[key].Secret {
			continue
		}
		value, err := s.store.Get(secrets.Key(where, name, key))
		if err != nil || value == "" {
			continue
		}
		if err := s.store.Set(secrets.Key(where, copyOf, key), value); err != nil {
			return EnvironmentDocument{}, fmt.Errorf("copying the value of %s: %w", key, err)
		}
	}
	if err := s.writeFile(target, duplicate); err != nil {
		return EnvironmentDocument{}, err
	}
	return s.Load(copyOf)
}

// Delete removes env/<name>.json.
//
// The values its secret references pointed at are left in the keychain: they
// belong to the machine, not to the file, and a teammate's branch may still
// reference them. RemoveVariable is the operation that forgets a value.
func (s *EnvironmentService) Delete(name string) (Environments, error) {
	root, _, err := s.read(name)
	if err != nil {
		return Environments{}, err
	}
	target, err := s.file(root, name)
	if err != nil {
		return Environments{}, err
	}
	release := s.collections.Guard().Writing(target)
	if err := os.Remove(target); err != nil {
		release()
		return Environments{}, fmt.Errorf("deleting %s: %w", resolve.EnvPath(name), err)
	}
	release()

	// The environment that was active has gone; leaving the name behind would
	// make every send fail against a file that is not there.
	if active, _ := s.active(); active == name {
		if _, err := s.settings.Update(func(v *settings.Settings) { v.ActiveEnv = "" }); err == nil {
			s.emit(events.SettingsChanged, nil)
		}
	}
	return s.announce()
}

// SetVariable writes a plain variable, creating it if it is new. A row that
// was a secret becomes plain and its keychain value is forgotten — the file
// would otherwise say the value is committed while the real one sat in the
// keychain, unused and unreachable.
func (s *EnvironmentService) SetVariable(envName, name, value string) (EnvironmentDocument, error) {
	if err := validVarName(name); err != nil {
		return EnvironmentDocument{}, err
	}
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		previous, existed := env.Values[name]
		kind := previous.Kind
		if !existed || previous.Secret {
			kind = resolve.EnvString
		}
		env.Values[name] = resolve.EnvValue{Value: value, Kind: kind}
		if !existed {
			env.Order = append(env.Order, name)
		}
		if previous.Secret {
			s.forget(secrets.Key(key, envName, name))
		}
		return nil
	})
}

// RenameVariable renames a variable in place, keeping its position in the
// file. A secret's stored value moves with it, so renaming does not silently
// orphan the value.
func (s *EnvironmentService) RenameVariable(envName, from, to string) (EnvironmentDocument, error) {
	if err := validVarName(to); err != nil {
		return EnvironmentDocument{}, err
	}
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		value, ok := env.Values[from]
		if !ok {
			return fmt.Errorf("%s has no variable %q", env.Path, from)
		}
		if from == to {
			return nil
		}
		if _, taken := env.Values[to]; taken {
			return fmt.Errorf("%s already has a variable %q", env.Path, to)
		}
		delete(env.Values, from)
		env.Values[to] = value
		for i, k := range env.Order {
			if k == from {
				env.Order[i] = to
			}
		}
		if value.Secret {
			// Move the value, then drop the old key. Copying first means a
			// failure leaves the value reachable under the old name rather
			// than nowhere.
			if current, err := s.store.Get(secrets.Key(key, envName, from)); err == nil {
				if err := s.store.Set(secrets.Key(key, envName, to), current); err != nil {
					return err
				}
				s.forget(secrets.Key(key, envName, from))
			}
		}
		return nil
	})
}

// RemoveVariable removes a variable from the file. A secret's stored value is
// removed from the keychain with it: a value nobody can see and nothing
// references is the worst of the three outcomes, so the reference and the
// value go together.
func (s *EnvironmentService) RemoveVariable(envName, name string) (EnvironmentDocument, error) {
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		value, ok := env.Values[name]
		if !ok {
			return fmt.Errorf("%s has no variable %q", env.Path, name)
		}
		delete(env.Values, name)
		env.Order = without(env.Order, name)
		if value.Secret {
			s.forget(secrets.Key(key, envName, name))
		}
		return nil
	})
}

// MakeSecret turns a variable into a {"$secret": "keychain"} reference and
// puts value in the keychain. It is also how a brand-new secret is added.
//
// The keychain write comes first: a file that references a value which is not
// there is a request that fails at send time, while a value in the keychain
// that nothing references yet is inert.
func (s *EnvironmentService) MakeSecret(envName, name, value string) (EnvironmentDocument, error) {
	if err := validVarName(name); err != nil {
		return EnvironmentDocument{}, err
	}
	if value == "" {
		return EnvironmentDocument{}, errors.New("a secret needs a value")
	}
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		if err := s.store.Set(secrets.Key(key, envName, name), value); err != nil {
			return err
		}
		if _, existed := env.Values[name]; !existed {
			env.Order = append(env.Order, name)
		}
		env.Values[name] = resolve.EnvValue{Secret: true, Kind: resolve.EnvSecret}
		return nil
	})
}

// SetSecretValue replaces the stored value of an existing secret reference.
// It is the design's "Replace value".
func (s *EnvironmentService) SetSecretValue(envName, name, value string) (EnvironmentDocument, error) {
	if value == "" {
		return EnvironmentDocument{}, errors.New("a secret needs a value")
	}
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		if v, ok := env.Values[name]; !ok || !v.Secret {
			return fmt.Errorf("%s: %q is not a secret", env.Path, name)
		}
		return s.store.Set(secrets.Key(key, envName, name), value)
	})
}

// ForgetSecret removes a secret's value from the keychain and leaves the
// reference in the file. It is the design's "Remove from keychain": the
// committed reference is the team's, the value is this machine's, and this
// gives up only the second.
func (s *EnvironmentService) ForgetSecret(envName, name string) (EnvironmentDocument, error) {
	return s.mutate(envName, func(env *resolve.Environment, key string) error {
		if v, ok := env.Values[name]; !ok || !v.Secret {
			return fmt.Errorf("%s: %q is not a secret", env.Path, name)
		}
		s.forget(secrets.Key(key, envName, name))
		return nil
	})
}

// CopySecretValue puts a secret's value on the system clipboard and returns
// nothing.
//
// This is the design's Reveal control (screen 1c), resolved the only way the
// secrets rule allows. Rendering the value would mean handing it to the
// webview, where it would sit in a React tree, a DOM node and any devtools
// session — so Otis does not. The clipboard write happens in Go: the user gets
// their own credential where they can paste it, and the value never crosses
// the binding. Nothing is returned but an error, and the error names the key.
func (s *EnvironmentService) CopySecretValue(envName, name string) error {
	root, env, err := s.read(envName)
	if err != nil {
		return err
	}
	if v, ok := env.Values[name]; !ok || !v.Secret {
		return fmt.Errorf("%s: %q is not a secret", env.Path, name)
	}
	key := secrets.Key(collection.DisplayName(root), envName, name)
	value, err := s.store.Get(key)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return fmt.Errorf("no value for %s is stored on this machine", key)
		}
		return err
	}
	if s.app == nil {
		return nil // not running under Wails, as in tests
	}
	if !s.app.Clipboard.SetText(value) {
		return fmt.Errorf("copying %s: the clipboard refused the text", key)
	}
	return nil
}

// SetConfirmBeforeSend writes the environment's "$otis" confirmBeforeSend
// setting. It is committed on purpose: the whole team should get the same
// warning on the same environment, and a change to it should show up in
// review.
func (s *EnvironmentService) SetConfirmBeforeSend(envName string, on bool) (EnvironmentDocument, error) {
	return s.mutate(envName, func(env *resolve.Environment, _ string) error {
		env.Meta.ConfirmBeforeSend = on
		return nil
	})
}

// SetDescription writes the environment's "$otis" description.
func (s *EnvironmentService) SetDescription(envName, description string) (EnvironmentDocument, error) {
	return s.mutate(envName, func(env *resolve.Environment, _ string) error {
		env.Meta.Description = strings.TrimSpace(description)
		return nil
	})
}

// mutate reads the environment, applies fn, writes it back through the write
// guard, and announces the result. fn receives the collection key so it can
// address the keychain.
func (s *EnvironmentService) mutate(name string, fn func(*resolve.Environment, string) error) (EnvironmentDocument, error) {
	root, env, err := s.read(name)
	if err != nil {
		return EnvironmentDocument{}, err
	}
	if err := fn(env, collection.DisplayName(root)); err != nil {
		return EnvironmentDocument{}, err
	}
	target, err := s.file(root, name)
	if err != nil {
		return EnvironmentDocument{}, err
	}
	if err := s.writeFile(target, env); err != nil {
		return EnvironmentDocument{}, err
	}
	return s.Load(name)
}

// writeFile writes an environment inside the write guard and tells the window.
func (s *EnvironmentService) writeFile(target string, env *resolve.Environment) error {
	// Inside the guard, like every other write Otis makes: otherwise the
	// watcher reports this save as somebody else's change.
	release := s.collections.Guard().Writing(target)
	err := writeFileAtomic(target, env.Marshal())
	release()
	if err != nil {
		return fmt.Errorf("saving %s: %w", env.Path, err)
	}
	// The guard means the watcher will not announce this, so the write does
	// it itself.
	if _, err := s.announce(); err != nil {
		return err
	}
	return nil
}

// announce emits the current environment list and returns it.
func (s *EnvironmentService) announce() (Environments, error) {
	list, err := s.List()
	if err != nil {
		return list, err
	}
	s.emit(events.EnvironmentsChanged, list)
	return list, nil
}

// read loads one environment and returns the collection root with it.
func (s *EnvironmentService) read(name string) (string, *resolve.Environment, error) {
	root, err := s.root()
	if err != nil {
		return "", nil, err
	}
	if err := validEnvName(name); err != nil {
		return "", nil, err
	}
	env, err := resolve.LoadEnvironment(root, name)
	if err != nil {
		return "", nil, err
	}
	return root, env, nil
}

func (s *EnvironmentService) root() (string, error) {
	root := s.collections.Current().Path
	if root == "" {
		return "", errors.New("no collection is open")
	}
	return root, nil
}

// file is the absolute path of an environment file, kept inside env/.
func (s *EnvironmentService) file(root, name string) (string, error) {
	if err := validEnvName(name); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(resolve.EnvPath(name))), nil
}

// active is the active environment's name, "" for none.
func (s *EnvironmentService) active() (string, error) {
	v, err := s.settings.Get()
	if err != nil {
		return "", err
	}
	return v.ActiveEnv, nil
}

// present reports whether a secret key has a value on this machine.
func (s *EnvironmentService) present(key string) bool {
	_, err := s.store.Get(key)
	return err == nil
}

// forget drops a stored secret, ignoring "there was none".
func (s *EnvironmentService) forget(key string) {
	if err := s.store.Delete(key); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		s.logError("removing a secret from the keychain", err)
	}
}

// keychain reports whether this machine's credential store can be reached.
func (s *EnvironmentService) keychain() KeychainState {
	probe, ok := s.store.(interface{ Available() error })
	if !ok {
		// A store with nothing to probe — the in-memory one under test, or
		// the CLI's environment variables — is always reachable.
		return KeychainState{Available: true}
	}
	if err := probe.Available(); err != nil {
		return KeychainState{Reason: err.Error()}
	}
	return KeychainState{Available: true}
}

func (s *EnvironmentService) emit(name string, data any) {
	if s.app == nil {
		return // not running under Wails, as in tests
	}
	s.app.Event.Emit(name, data)
}

func (s *EnvironmentService) logError(msg string, err error) {
	recordError(s.app, "environment", msg, err)
}

// validEnvName rejects a name that is not a bare file name. FORMAT.md §4.3:
// the environment name is the file name without .json and must not contain
// path separators.
func validEnvName(name string) error {
	if name == "" {
		return errors.New("an environment needs a name")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid environment name %q", name)
	}
	return nil
}

// validVarName rejects a name that could not be referenced as {{name}}
// (docs/FORMAT.md §4.1) and the reserved "$otis" key (§4.3). A name the
// resolver can never match would be a row that silently does nothing.
func validVarName(name string) error {
	if name == "" {
		return errors.New("a variable needs a name")
	}
	if name == resolve.MetaKey {
		return fmt.Errorf("%q is reserved for the environment's own settings", resolve.MetaKey)
	}
	if !resolve.ValidReferenceName(name) {
		return fmt.Errorf("%q is not a name a {{reference}} can use", name)
	}
	return nil
}

// without returns keys with name removed, preserving order.
func without(keys []string, name string) []string {
	out := keys[:0:0]
	for _, k := range keys {
		if k != name {
			out = append(out, k)
		}
	}
	return out
}
