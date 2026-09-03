package script

import (
	"github.com/dop251/goja"
)

// VarStore is what the runtime needs from the variable layer.
//
// An interface rather than the resolver itself, so this package does not
// depend on the send pipeline and a test can drive it with a map. Reads go
// through Resolve, which is the resolver's own §4.2 order — a script and a
// {{reference}} must never disagree about what a name means.
type VarStore interface {
	// Resolve is vars.get(k): the full fall-through of docs/FORMAT.md §4.2.
	// ok is false for a name nothing defines.
	Resolve(name string) (value string, ok bool)
	// ReadScope is vars.<scope>.get(k): one scope, no fall-through.
	ReadScope(scope VarScope, name string) (value string, ok bool)
	// WriteScope is vars.<scope>.set(k, v).
	WriteScope(scope VarScope, name, value string) error
}

// VarScope is one of the three lifetimes of docs/FORMAT.md §9.4.
type VarScope string

const (
	// ScopeRequest lives for this send only and is written nowhere.
	ScopeRequest VarScope = "request"
	// ScopeSession lives until the collection closes, in memory on this
	// machine, keyed by the request's folder (docs/FORMAT.md §4.5).
	//
	// Named "session" and not "folder" on purpose. `_folder.http` declares
	// committed variables with `@name = value`, and `vars.folder.set` reads as
	// setting one of those — it does not, it sets a value that is in no file.
	// §4.5 already calls the thing a session variable; the API says the same
	// word.
	ScopeSession VarScope = "session"
	// ScopeEnv writes the active environment file, which is committed. It is
	// the one call in the API that changes a file somebody will review.
	ScopeEnv VarScope = "env"
)

// varsAPI installs `vars`.
func (r *Runtime) varsAPI() (*goja.Object, error) {
	obj := r.vm.NewObject()

	// vars.get resolves with the full fall-through, which is the whole point:
	// a script reading a name and a header interpolating it get the same
	// answer because they ask the same thing.
	if err := obj.Set("get", func(name string) goja.Value {
		if r.opts.Vars == nil {
			return goja.Undefined()
		}
		value, ok := r.opts.Vars.Resolve(name)
		if !ok {
			return goja.Undefined()
		}
		return r.vm.ToValue(value)
	}); err != nil {
		return nil, err
	}

	for _, scope := range []VarScope{ScopeRequest, ScopeSession, ScopeEnv} {
		api, err := r.scopeAPI(scope)
		if err != nil {
			return nil, err
		}
		if err := obj.Set(string(scope), api); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

// scopeAPI installs one of `vars.request`, `vars.session`, `vars.env`.
func (r *Runtime) scopeAPI(scope VarScope) (*goja.Object, error) {
	obj := r.vm.NewObject()

	if err := obj.Set("set", func(name string, value goja.Value) {
		if r.opts.Vars == nil {
			panic(r.vm.NewTypeError("vars.%s.set: no variable store is available", scope))
		}
		// A secret handle must not be stored. The handle is what keeps a
		// value out of the places it should not be, and a variable scope is
		// one of those places: a session value is shown in the folder view,
		// and an environment value is written to a committed file.
		if h := r.handleOf(value); h != nil {
			panic(r.vm.NewTypeError(
				"vars.%s.set(%q): a secret cannot be stored in a variable — use secrets.ref(%q) where the value is needed",
				scope, name, h.Name))
		}
		text, err := r.varText(scope, name, value)
		if err != nil {
			panic(r.vm.NewTypeError("%s", err.Error()))
		}
		if err := r.opts.Vars.WriteScope(scope, name, text); err != nil {
			panic(r.vm.NewTypeError("vars.%s.set(%q): %s", scope, name, err.Error()))
		}
	}); err != nil {
		return nil, err
	}

	// get on a scope reads that scope alone, which is how a script asks "did
	// I set this?" rather than "what would resolve here?".
	if err := obj.Set("get", func(name string) goja.Value {
		if r.opts.Vars == nil {
			return goja.Undefined()
		}
		value, ok := r.opts.Vars.ReadScope(scope, name)
		if !ok {
			return goja.Undefined()
		}
		return r.vm.ToValue(value)
	}); err != nil {
		return nil, err
	}
	return obj, nil
}

// varText converts a value for storage, refusing the shapes that are almost
// always a bug.
//
// null, undefined and a function are errors rather than "null", "undefined"
// and a source dump: a header reading `undefined` is a worse outcome than a
// message saying which call produced it.
func (r *Runtime) varText(scope VarScope, name string, value goja.Value) (string, error) {
	switch {
	case value == nil || goja.IsUndefined(value):
		return "", errorf("vars.%s.set(%q): the value is undefined", scope, name)
	case goja.IsNull(value):
		return "", errorf("vars.%s.set(%q): the value is null", scope, name)
	}
	if _, ok := goja.AssertFunction(value); ok {
		return "", errorf("vars.%s.set(%q): a function is not a value", scope, name)
	}
	if obj, ok := value.(*goja.Object); ok {
		switch obj.ClassName() {
		case "Object", "Array":
			// An object is stringified by JSON rather than as
			// "[object Object]", which is what somebody storing a body
			// fragment actually meant.
			data, err := value.ToObject(r.vm).MarshalJSON()
			if err != nil {
				return "", errorf("vars.%s.set(%q): the value cannot be stored: %s", scope, name, err.Error())
			}
			return string(data), nil
		}
	}
	return value.String(), nil
}

// errorf is fmt.Errorf without the import, kept local so the panic sites read
// as one line.
func errorf(format string, args ...any) error { return &varError{msg: sprintf(format, args...)} }

type varError struct{ msg string }

func (e *varError) Error() string { return e.msg }
