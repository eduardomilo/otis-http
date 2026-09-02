package resolve

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otis-http/otis/internal/collection"
)

// EnvExt is the extension of environment files under env/.
const EnvExt = ".json"

// SecretBackendKeychain is the only supported $secret backend.
const SecretBackendKeychain = "keychain"

// EnvValue is one entry of an environment file.
type EnvValue struct {
	// Value is the literal value. Empty when Secret is set.
	Value string
	// Secret marks a {"$secret": "keychain"} reference: the value is looked
	// up in the secrets store under <collection>/<env>/<name>.
	Secret bool
}

// Environment is a parsed env/<name>.json: a flat map of variable names to
// values or secret references.
type Environment struct {
	Name string
	// Path is relative to the collection root, e.g. "env/dev.json".
	Path   string
	Values map[string]EnvValue
}

// Names returns the variable names in sorted order.
func (e *Environment) Names() []string {
	names := make([]string, 0, len(e.Values))
	for n := range e.Values {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SecretNames returns the names whose values are secret references, sorted.
func (e *Environment) SecretNames() []string {
	var names []string
	for n, v := range e.Values {
		if v.Secret {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// EnvPath returns the path of the environment file for name, relative to
// the collection root.
func EnvPath(name string) string {
	return path.Join(collection.EnvDirName, name+EnvExt)
}

// LoadEnvironment reads env/<name>.json from the collection rooted at dir.
func LoadEnvironment(dir, name string) (*Environment, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return nil, fmt.Errorf("invalid environment name %q", name)
	}
	rel := EnvPath(name)
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("environment %q not found (expected %s)", name, rel)
		}
		return nil, err
	}
	env, err := ParseEnvironment(name, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return env, nil
}

// ListEnvironments returns the environment names available in the
// collection rooted at dir, sorted. A missing env/ directory yields none.
func ListEnvironments(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, collection.EnvDirName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), EnvExt) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), EnvExt))
	}
	sort.Strings(names)
	return names, nil
}

// ParseEnvironment parses environment JSON. Values must be strings, numbers,
// booleans, or the object {"$secret": "keychain"}. Numbers and booleans are
// stringified as written.
func ParseEnvironment(name string, data []byte) (*Environment, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid JSON: unexpected content after the object")
	}
	env := &Environment{Name: name, Path: EnvPath(name), Values: make(map[string]EnvValue, len(raw))}
	for key, msg := range raw {
		v, err := parseEnvValue(msg)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		env.Values[key] = v
	}
	return env, nil
}

func parseEnvValue(msg json.RawMessage) (EnvValue, error) {
	dec := json.NewDecoder(strings.NewReader(string(msg)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return EnvValue{}, err
	}
	switch x := v.(type) {
	case string:
		return EnvValue{Value: x}, nil
	case json.Number:
		return EnvValue{Value: x.String()}, nil
	case bool:
		return EnvValue{Value: fmt.Sprintf("%t", x)}, nil
	case nil:
		return EnvValue{}, fmt.Errorf("value must be a string, not null")
	case map[string]any:
		backend, ok := x["$secret"]
		if !ok || len(x) != 1 {
			return EnvValue{}, fmt.Errorf(`value must be a string or {"$secret": "keychain"}`)
		}
		if backend != SecretBackendKeychain {
			return EnvValue{}, fmt.Errorf("unsupported $secret backend %v: only %q is supported", backend, SecretBackendKeychain)
		}
		return EnvValue{Secret: true}, nil
	default:
		return EnvValue{}, fmt.Errorf("value must be a string, not %T", v)
	}
}
