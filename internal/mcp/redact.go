package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The redaction boundary.
//
// docs/MCP.md §2 and VISION.md §3 both put one line above every other rule in
// this package: a resolved secret value never leaves Go. An agent is the
// hardest case that line has faced. The window is one process the user is
// looking at; an agent's transcript is retained, replayed, summarised, and
// sent to a model provider. Everything else in the MCP design is recoverable
// — a bad send can be re-run, a bad write is in git — but a secret that
// reaches an agent is disclosed for good, so this file is written to fail
// closed rather than to be careful.
//
// Two decisions follow from that:
//
// **Masking happens at serialization, not per field.** A tool that builds its
// result and masks the three fields it knows about is one new field away from
// a leak, and the new field is added by whoever is not thinking about
// secrets. So every result goes through Marshal, which walks the whole JSON
// tree. There is nothing for a tool author to remember.
//
// **The output is verified, not assumed.** Marshal re-parses the bytes it is
// about to return and refuses if any secret survived. That check does not
// trust the walk above it: the walk is the thing being checked. Refusing
// costs an agent one error message; not refusing costs a credential.

// ErrSecretSurvived means redaction produced output that still carried a
// secret value, so the output was withheld.
//
// It is returned instead of bytes, never alongside them. Nothing calls this
// expecting to recover: reaching it means a bug in this file, and the correct
// behaviour on a bug here is to emit nothing.
var ErrSecretSurvived = errors.New("mcp: redaction failed, output withheld")

// A Redactor masks the secret values used by one request in anything derived
// from it that leaves Go.
//
// It holds a masking function rather than the values themselves, so the raw
// values stay where they already live — inside the *resolve.Resolved that
// resolved them — and this package never has a field a debugger or a log line
// could read them out of.
type Redactor struct {
	// mask is resolve.(*Resolved).Mask, or nil when nothing is masked.
	mask func(string) string
}

// NewRedactor returns a Redactor over a masking function, normally
// resolve.(*Resolved).Mask.
func NewRedactor(mask func(string) string) *Redactor { return &Redactor{mask: mask} }

// NoSecrets returns a Redactor for a result no secret went into: a listing, a
// tree, a parse error. It still marshals through the same path, so a tool
// cannot be written against a second, unverified one.
func NoSecrets() *Redactor { return &Redactor{} }

// Text masks a single string.
//
// Prefer Marshal. Text exists for the places a string is the whole payload —
// an error message, an elicitation prompt — and it does not verify its own
// output, because there is no structure to re-parse. Anything with fields
// goes through Marshal.
func (r *Redactor) Text(s string) string {
	if r == nil || r.mask == nil {
		return s
	}
	return r.mask(s)
}

// Marshal serializes v for an agent with every secret value masked.
//
// It returns an error rather than bytes if any secret survives, so a caller
// that ignores the error emits nothing.
func (r *Redactor) Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mcp: encoding a tool result: %w", err)
	}
	if r == nil || r.mask == nil {
		return raw, nil
	}

	tree, err := decode(raw)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(r.redact(tree))
	if err != nil {
		return nil, fmt.Errorf("mcp: re-encoding a redacted result: %w", err)
	}
	if err := r.verify(out); err != nil {
		return nil, err
	}
	return out, nil
}

// decode parses JSON keeping numbers as json.Number.
//
// That is what lets one masking rule cover the whole document. A secret can be
// entirely digits — an account number, a PIN — and would then survive a walk
// that only inspects strings, because it arrives as a float64. Decoded as
// json.Number it is text like everything else, and redact masks it by the same
// comparison.
func decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("mcp: re-reading a tool result: %w", err)
	}
	return tree, nil
}

// redact returns v with every secret value in it replaced.
//
// Keys are masked as well as values. A tool that returns a map of header name
// to value is one confusion away from returning it the other way round, and a
// walk that trusts keys would ship the credential as the key.
func (r *Redactor) redact(v any) any {
	switch t := v.(type) {
	case string:
		return r.mask(t)
	case json.Number:
		// A number becomes a string only if it *was* a secret; masking it in
		// place would produce "•••••" where a number is expected, which is
		// the correct trade — a type an agent did not expect beats a
		// credential it did.
		if masked := r.mask(t.String()); masked != t.String() {
			return masked
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[r.mask(k)] = r.redact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.redact(val)
		}
		return out
	default:
		// bool and nil hold no text, and decode leaves nothing else.
		return v
	}
}

// verify re-parses the bytes about to be returned and reports whether any
// secret value is still in them.
//
// It works by masking again: masking is idempotent, so if a second pass over
// the outbound bytes changes anything, the first pass missed it. That is why
// this can check for values it is not allowed to hold.
//
// There is deliberately no scan of the raw bytes for the secret. Every place
// JSON can carry arbitrary text is a string or a key, both of which this
// visits after unescaping — so a byte scan would add no coverage, and would
// reject every result whenever a secret happened to be a single character
// like a comma.
func (r *Redactor) verify(out []byte) error {
	tree, err := decode(out)
	if err != nil {
		return err
	}
	var survivor string
	if !r.clean(tree, &survivor) {
		// The error names where, and cannot name what: including the
		// surviving value would put it in the log line this exists to
		// prevent.
		return fmt.Errorf("%w (%s)", ErrSecretSurvived, survivor)
	}
	return nil
}

// clean reports whether nothing under v still masks to something different,
// setting where to the location of the first thing that does.
func (r *Redactor) clean(v any, where *string) bool {
	switch t := v.(type) {
	case string:
		if r.mask(t) != t {
			*where = "in a string value"
			return false
		}
	case json.Number:
		if r.mask(t.String()) != t.String() {
			*where = "in a number"
			return false
		}
	case map[string]any:
		for k, val := range t {
			if r.mask(k) != k {
				*where = "in an object key"
				return false
			}
			if !r.clean(val, where) {
				*where += " under key " + strconv(k)
				return false
			}
		}
	case []any:
		for i, val := range t {
			if !r.clean(val, where) {
				*where += fmt.Sprintf(" at index %d", i)
				return false
			}
		}
	}
	return true
}

// strconv quotes a key for an error message without dragging in a dependency
// for one call. A key is not secret — clean checked it first — but it can
// contain anything, so it is quoted.
func strconv(s string) string {
	if len(s) > 40 {
		s = s[:40] + "…"
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
