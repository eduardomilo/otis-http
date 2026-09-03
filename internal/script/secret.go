package script

import (
	"fmt"

	"github.com/dop251/goja"

	"github.com/otis-http/otis/internal/secrets"
)

// MaskFormat is what a secret handle turns into anywhere JavaScript would
// make text of it.
const MaskFormat = "[secret:%s]"

// Mask is the text a handle for name yields.
func Mask(name string) string { return fmt.Sprintf(MaskFormat, name) }

// Handle is an opaque reference to a secret value.
//
// # The invariant
//
// The value is not reachable from JavaScript. Not by printing the handle, not
// by interpolating it, not by adding it to a string, not by JSON.stringify,
// not by throwing it, not by asserting on it. Every one of those paths goes
// through the same three JavaScript coercion hooks — toString, valueOf and
// Symbol.toPrimitive — and all three return the mask. There is no fourth
// path: JavaScript has no way to make text of an object that does not use one
// of them.
//
// The real value lives here, in Go, and is read exactly once: when the
// request is prepared, after every script has run. That is why a handle is a
// *whole* value rather than something to concatenate — `"Bearer " + handle`
// is a coercion, so it yields the mask, which is the feature and not a
// limitation. prefix and suffix compose handles instead, so the value never
// becomes a JavaScript string on the way to the wire.
type Handle struct {
	// Name is the variable the secret is referenced by. It is public: it is
	// already committed in an environment file.
	Name string
	// prefix and suffix are the literal text around the value, from
	// .prefix() and .suffix().
	prefix string
	suffix string
	// value is the secret. It is unexported and there is no accessor that
	// JavaScript can reach; Reveal is Go-only and is called once, by the
	// sender.
	value string
}

// String is the mask. It is what fmt, a log line and an error message get,
// so a handle that reaches one of those by accident still cannot leak.
func (h *Handle) String() string { return h.masked() }

// masked is the handle as text: the literal parts as written, the secret as
// its mask.
func (h *Handle) masked() string { return h.prefix + Mask(h.Name) + h.suffix }

// Reveal returns the real text: the literal parts with the secret between
// them. Go only, and only the sender calls it.
func (h *Handle) Reveal() string { return h.prefix + h.value + h.suffix }

// secretsAPI installs the `secrets` global.
func (r *Runtime) secretsAPI() (*goja.Object, error) {
	api := r.vm.NewObject()
	if err := api.Set("ref", r.secretRef); err != nil {
		return nil, err
	}
	// has answers "is a value stored on this machine", which is a question
	// with a safe answer and the one a script legitimately needs before it
	// decides to use a secret at all.
	if err := api.Set("has", func(name string) bool {
		if r.opts.Env == nil {
			return false
		}
		v, ok := r.opts.Env.Values[name]
		if !ok || !v.Secret {
			return false
		}
		_, err := r.opts.Secrets.Get(secrets.Key(r.opts.Collection, r.opts.Env.Name, name))
		return err == nil
	}); err != nil {
		return nil, err
	}
	return api, nil
}

// secretRef builds a handle, or throws when the name is not a secret.
func (r *Runtime) secretRef(name string) goja.Value {
	if r.opts.Env == nil {
		panic(r.vm.NewTypeError("secrets.ref(%q): no environment is active, so nothing declares it a secret", name))
	}
	v, ok := r.opts.Env.Values[name]
	if !ok {
		panic(r.vm.NewTypeError("secrets.ref(%q): %s does not declare it", name, r.opts.Env.Path))
	}
	if !v.Secret {
		panic(r.vm.NewTypeError("secrets.ref(%q): %s declares it as a plain value, not a secret", name, r.opts.Env.Path))
	}
	key := secrets.Key(r.opts.Collection, r.opts.Env.Name, name)
	value, err := r.opts.Secrets.Get(key)
	if err != nil {
		// The key, never the value — and there is no value to name anyway.
		panic(r.vm.NewTypeError("secrets.ref(%q): no value for %s is stored on this machine", name, key))
	}
	return r.wrapHandle(&Handle{Name: name, value: value})
}

// wrapHandle turns a Handle into the JavaScript object a script sees.
//
// Every coercion hook is overridden and every one returns the mask. The
// handle itself is kept in a hidden Go field so the sender can find it again;
// there is no JavaScript-visible property that carries the value.
func (r *Runtime) wrapHandle(h *Handle) goja.Value {
	obj := r.vm.NewObject()
	r.handles[obj] = h

	mask := r.vm.ToValue(h.masked())
	must := func(err error) {
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
	}
	// Every property is non-enumerable, which is not tidiness. An enumerable
	// one puts the handle's internals into Object.keys, Object.values, a
	// for-in and a spread — and Object.values on a Go-backed function prints
	// its Go symbol, so the window would show
	// "internal/script.(*Runtime).wrapHandle.func2" to somebody debugging a
	// script. Nothing about the handle's shape is a script's business.
	hidden := func(name string, value any) {
		must(obj.DefineDataProperty(name, r.vm.ToValue(value),
			goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE))
	}

	// The three coercion hooks. There is no fourth.
	hidden("toString", func() string { return h.masked() })
	hidden("valueOf", func() string { return h.masked() })
	must(obj.DefineDataPropertySymbol(goja.SymToPrimitive,
		r.vm.ToValue(func(goja.FunctionCall) goja.Value { return mask }),
		goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE))
	// JSON.stringify calls toJSON before it does anything else.
	hidden("toJSON", func() string { return h.masked() })
	// Node's inspection hook, in case a console implementation reaches for it.
	hidden("inspect", func() string { return h.masked() })

	// Composition, so "Bearer <secret>" never becomes a JavaScript string.
	hidden("prefix", func(text string) goja.Value {
		return r.wrapHandle(&Handle{Name: h.Name, prefix: text + h.prefix, suffix: h.suffix, value: h.value})
	})
	hidden("suffix", func(text string) goja.Value {
		return r.wrapHandle(&Handle{Name: h.Name, prefix: h.prefix, suffix: h.suffix + text, value: h.value})
	})
	// The name is public: it is already committed in an environment file.
	hidden("name", h.Name)

	// A marker a script can read but not use: request.headers.set tells a
	// handle from a string by the Go map, not by this, so it is documentation
	// rather than mechanism — a script that wants to branch on "is this a
	// secret" should be able to.
	hidden("isSecret", true)
	return obj
}

// handleOf returns the Handle behind a value, or nil when it is not one.
func (r *Runtime) handleOf(v goja.Value) *Handle {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	return r.handles[obj]
}

// text converts a script value to the string Otis will *show*, and reports
// whether it held a secret.
//
// A handle yields its mask here, not its value: this is the conversion that
// feeds console output and test messages. Only the sender, through Reveal,
// ever sees the other one.
//
// undefined and null render as "undefined" and "null", the way JavaScript
// would, because this is a display conversion and printing an empty string
// for them would hide a bug rather than show it. The setters that must not
// accept them — a variable, a header, a body — refuse them before they get
// here.
func (r *Runtime) text(v goja.Value) (string, bool) {
	if h := r.handleOf(v); h != nil {
		return h.masked(), true
	}
	switch {
	case v == nil || goja.IsUndefined(v):
		return "undefined", false
	case goja.IsNull(v):
		return "null", false
	}
	return v.String(), false
}
