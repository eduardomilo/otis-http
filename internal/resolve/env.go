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

// EnvKind is the JSON shape a value was written as. It is recorded so writing
// the file back preserves it: docs/FORMAT.md §4.3 says a number is used as
// written, and turning 8443 into "8443" on the first save would be exactly the
// kind of gratuitous diff §1.13 exists to prevent.
type EnvKind string

const (
	EnvString EnvKind = "string"
	EnvNumber EnvKind = "number"
	EnvBool   EnvKind = "bool"
	// EnvSecret is a {"$secret": "keychain"} reference.
	EnvSecret EnvKind = "secret"
)

// EnvValue is one entry of an environment file.
type EnvValue struct {
	// Value is the literal value, stringified for a number or a boolean.
	// Empty when Secret is set.
	Value string
	// Secret marks a {"$secret": "keychain"} reference: the value is looked
	// up in the secrets store under <collection>/<env>/<name>.
	Secret bool
	// Kind is the JSON shape to write the value back as. The zero value is
	// EnvString, so a value built in code needs no ceremony.
	Kind EnvKind
}

// EnvMeta is the environment's own settings, held under the reserved "$otis"
// key (docs/FORMAT.md §4.3). It is committed, so the whole team gets the same
// warning on the same environment, and it shows up in review when somebody
// changes it.
type EnvMeta struct {
	// ConfirmBeforeSend asks for a confirmation before every send resolved
	// against this environment. It is what marks production: the design
	// paints such an environment's dot red (DESIGN-NOTES §2.6) and the
	// command palette says "confirms before send" on its row (screen 2c).
	ConfirmBeforeSend bool `json:"confirmBeforeSend,omitempty"`
	// Description is a one-line note shown beside the environment.
	Description string `json:"description,omitempty"`
	// Agents is the MCP agent policy for this environment (docs/MCP.md §4):
	// AgentDeny, AgentConfirm or AgentAllow. Empty means unset, which reads
	// as AgentConfirm — an environment that says nothing gets a person in the
	// loop, and opting out is the deliberate act.
	Agents AgentPolicy `json:"agents,omitempty"`

	// Extra holds fields of "$otis" this version does not know, so writing
	// the file back does not drop them.
	//
	// The same bargain section 1.4 makes for an unknown directive: preserved
	// and ignored. Without it, opening a collection in an older Otis and
	// changing one variable would silently delete a setting a newer one
	// wrote, which is the kind of data loss nobody attributes to the right
	// cause.
	Extra map[string]json.RawMessage `json:"-"`
}

// known are the "$otis" fields this version owns; everything else goes to
// Extra.
var knownMetaFields = map[string]bool{
	"confirmBeforeSend": true,
	"description":       true,
	"agents":            true,
}

// AgentPolicy is what an environment permits an MCP agent to do
// (docs/MCP.md §4). It is committed, deliberately: whether an environment is
// the dangerous one is a fact about the environment and not a per-machine
// preference, so the whole team gets the same answer, a fresh clone gets it
// without configuring anything, and weakening it shows up in review.
type AgentPolicy string

const (
	// AgentUnset is an environment with no "agents" field. It reads as
	// AgentConfirm; see EffectiveAgentPolicy.
	AgentUnset AgentPolicy = ""
	// AgentDeny refuses every agent send against this environment.
	AgentDeny AgentPolicy = "deny"
	// AgentConfirm asks a person before every agent send. The default.
	AgentConfirm AgentPolicy = "confirm"
	// AgentAllow lets an agent send without asking. It cannot override
	// ConfirmBeforeSend, and it cannot override the review gate
	// (docs/MCP.md §5) or mcp.alwaysConfirmSends.
	AgentAllow AgentPolicy = "allow"
)

// EffectiveAgentPolicy is the environment's policy with its default applied.
//
// Unset reads as AgentConfirm rather than AgentAllow: an environment that says
// nothing about agents gets a person in the loop, and opting out is the
// deliberate act (docs/MCP.md §4 rule 1).
func (m EnvMeta) EffectiveAgentPolicy() AgentPolicy {
	if m.Agents == AgentUnset {
		return AgentConfirm
	}
	return m.Agents
}

// validateAgents rejects a value that is not one of the three.
//
// An error rather than a silent fallback, because "alow" must not read as
// permission (docs/MCP.md §4). And "allow" beside confirmBeforeSend is an
// error too: that flag is the committed marker of production, and an agent
// policy able to downgrade it would let a convenience setting cancel a safety
// one.
func validateAgents(m EnvMeta, name string) error {
	switch m.Agents {
	case AgentUnset, AgentDeny, AgentConfirm:
	case AgentAllow:
		if m.ConfirmBeforeSend {
			return fmt.Errorf(
				"%s: $otis.agents is \"allow\" but confirmBeforeSend is true; "+
					"an agent policy cannot downgrade a confirmation the environment asks for", name)
		}
	default:
		return fmt.Errorf(
			"%s: $otis.agents is %q; it must be \"deny\", \"confirm\" or \"allow\"", name, m.Agents)
	}
	return nil
}

// IsZero reports whether the meta carries nothing, in which case no "$otis"
// key is written at all.
func (m EnvMeta) IsZero() bool {
	return !m.ConfirmBeforeSend && m.Description == "" && m.Agents == AgentUnset &&
		len(m.Extra) == 0
}

// parseMeta reads the "$otis" object, keeping any field it does not know.
func parseMeta(msg json.RawMessage) (EnvMeta, error) {
	var meta EnvMeta
	if err := json.Unmarshal(msg, &meta); err != nil {
		return EnvMeta{}, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(msg, &all); err != nil {
		return EnvMeta{}, err
	}
	for key, value := range all {
		if knownMetaFields[key] {
			continue
		}
		if meta.Extra == nil {
			meta.Extra = map[string]json.RawMessage{}
		}
		meta.Extra[key] = value
	}
	return meta, nil
}

// marshalMeta writes the "$otis" object: the known fields, then the
// preserved unknown ones, all in sorted key order so the bytes are stable.
func marshalMeta(m EnvMeta) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	for key, value := range m.Extra {
		fields[key] = value
	}
	if m.ConfirmBeforeSend {
		fields["confirmBeforeSend"] = json.RawMessage("true")
	} else {
		delete(fields, "confirmBeforeSend")
	}
	if m.Description != "" {
		fields["description"] = json.RawMessage(quote(m.Description))
	} else {
		delete(fields, "description")
	}
	if m.Agents != AgentUnset {
		fields["agents"] = json.RawMessage(quote(string(m.Agents)))
	} else {
		delete(fields, "agents")
	}

	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(quote(key))
		b.WriteString(":")
		b.Write(fields[key])
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// MetaKey is the reserved environment key holding EnvMeta.
const MetaKey = "$otis"

// Environment is a parsed env/<name>.json: a flat map of variable names to
// values or secret references.
type Environment struct {
	Name string
	// Path is relative to the collection root, e.g. "env/dev.json".
	Path   string
	Values map[string]EnvValue
	// Order is the keys in the order the file listed them, so writing the
	// file back does not reshuffle it. Keys absent from Order are written
	// after it, sorted.
	Order []string
	// Meta is the "$otis" entry, or the zero value when the file has none.
	Meta EnvMeta
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

// OrderedNames returns the variable names in the order the file lists them,
// with any name the order does not mention after it, sorted.
//
// This is what a surface showing the file should use. Names() sorts, which is
// right for a report; the editor's table has to match what is on disk, or
// saving after an edit would look like a reshuffle.
func (e *Environment) OrderedNames() []string { return e.keysInWriteOrder() }

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
// stringified as written. The reserved "$otis" key carries EnvMeta.
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
	order, err := keyOrder(data)
	if err != nil {
		return nil, err
	}
	env := &Environment{Name: name, Path: EnvPath(name), Values: make(map[string]EnvValue, len(raw))}
	for _, key := range order {
		msg, ok := raw[key]
		if !ok {
			continue // a duplicate key; the decoder kept the last one
		}
		if key == MetaKey {
			meta, err := parseMeta(msg)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			if err := validateAgents(meta, "key "+quote(key)); err != nil {
				return nil, err
			}
			env.Meta = meta
			continue
		}
		// The "$" namespace is reserved so a later version can add a second
		// settings key without any collection having to be migrated. Better
		// an error now, while nobody has written one, than a name that means
		// two things later.
		if strings.HasPrefix(key, "$") {
			return nil, fmt.Errorf("key %q: keys beginning with \"$\" are reserved; only %q is defined", key, MetaKey)
		}
		v, err := parseEnvValue(msg)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		env.Values[key] = v
		env.Order = append(env.Order, key)
	}
	return env, nil
}

// keyOrder returns the object's keys in the order the bytes list them.
//
// encoding/json unmarshals an object into a map, which has no order, and the
// order is what keeps a save out of somebody's diff. A duplicate key appears
// once, at its first position, which matches the decoder keeping the last
// value for it: the pair is then the same entry the file already had.
func keyOrder(data []byte) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("invalid JSON: an environment must be an object")
	}
	var order []string
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON: an object key must be a string")
		}
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
		// Skip the value, whatever shape it is.
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
	}
	return order, nil
}

// Marshal writes the environment as the JSON that belongs in the file.
//
// Canonical form, the same bargain as docs/FORMAT.md §1.13 makes for a .http
// file: two-space indent, keys in the file's own order with new ones appended
// (sorted) after, values written in the shape they were read in, and the
// reserved "$otis" key first when there is one. Parsing a canonical file and
// writing it back produces identical bytes, so editing one variable does not
// reshuffle a teammate's file.
func (e *Environment) Marshal() []byte {
	var b strings.Builder
	b.WriteString("{\n")

	entries := make([]string, 0, len(e.Values)+1)
	if !e.Meta.IsZero() {
		if meta, err := marshalMeta(e.Meta); err == nil {
			entries = append(entries, fmt.Sprintf("  %s: %s", quote(MetaKey), meta))
		}
	}
	for _, key := range e.keysInWriteOrder() {
		entries = append(entries, fmt.Sprintf("  %s: %s", quote(key), encodeEnvValue(e.Values[key])))
	}

	b.WriteString(strings.Join(entries, ",\n"))
	if len(entries) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// keysInWriteOrder is Order, filtered to keys that still exist, then every
// other key sorted. A variable added by the editor lands at the end, which is
// where a person adding one by hand would put it.
func (e *Environment) keysInWriteOrder() []string {
	keys := make([]string, 0, len(e.Values))
	written := map[string]bool{}
	for _, key := range e.Order {
		if _, ok := e.Values[key]; ok && !written[key] {
			written[key] = true
			keys = append(keys, key)
		}
	}
	var rest []string
	for key := range e.Values {
		if !written[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// encodeEnvValue writes one value in the JSON shape it was read in. A number
// or boolean that is no longer valid JSON — the editor was handed "abc" for a
// key that used to hold 8443 — falls back to a string rather than writing a
// file that will not parse.
func encodeEnvValue(v EnvValue) string {
	if v.Secret {
		return fmt.Sprintf(`{"$secret": %s}`, quote(SecretBackendKeychain))
	}
	switch v.Kind {
	case EnvNumber:
		if json.Valid([]byte(v.Value)) && isJSONNumber(v.Value) {
			return v.Value
		}
	case EnvBool:
		if v.Value == "true" || v.Value == "false" {
			return v.Value
		}
	}
	return quote(v.Value)
}

// isJSONNumber reports whether s is a bare JSON number, so a value read as one
// is written back as one.
func isJSONNumber(s string) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return false
	}
	_, ok := v.(json.Number)
	return ok
}

// quote is encoding/json's string escaping without the HTML escaping, which
// would turn a URL's & into & in a file people read.
func quote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(s) != nil {
		return `""`
	}
	return strings.TrimSuffix(b.String(), "\n")
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
		return EnvValue{Value: x, Kind: EnvString}, nil
	case json.Number:
		return EnvValue{Value: x.String(), Kind: EnvNumber}, nil
	case bool:
		return EnvValue{Value: fmt.Sprintf("%t", x), Kind: EnvBool}, nil
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
		return EnvValue{Secret: true, Kind: EnvSecret}, nil
	default:
		return EnvValue{}, fmt.Errorf("value must be a string, not %T", v)
	}
}
