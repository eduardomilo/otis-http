package script

import (
	"fmt"
	"time"

	"github.com/dop251/goja"

	"github.com/otis-http/otis/internal/resolve"
)

// forbidden are the globals a script must never have (docs/FORMAT.md §9.3).
//
// goja does not provide any of them — there is no filesystem, process or
// network in a bare realm — so this list is not what removes them. It is what
// stops one from arriving: they are defined as throwing stubs, so a script
// that reaches for `fetch` gets a message telling it why it cannot rather than
// "fetch is not defined", and a future change that wires one in has to delete
// a line here first.
var forbidden = map[string]string{
	"require":        "modules are imported, not required: use `import { x } from \"../lib/y.js\"` (docs/FORMAT.md §9.8)",
	"fetch":          "a script shapes the request Otis is about to send; it cannot send one of its own",
	"XMLHttpRequest": "a script shapes the request Otis is about to send; it cannot send one of its own",
	"WebSocket":      "a script shapes the request Otis is about to send; it cannot send one of its own",
	"process":        "there is no process, no environment and no filesystem in a script",
	"setTimeout":     "a send is synchronous: a script that could wait could hang one forever",
	"setInterval":    "a send is synchronous: a script that could wait could hang one forever",
	"importScripts":  "modules are imported, not loaded: see docs/FORMAT.md §9.8",
}

// install puts the §9.3 globals in the realm and the forbidden ones out of it.
func (r *Runtime) install() error {
	set := func(name string, value any) error {
		if err := r.vm.Set(name, value); err != nil {
			return fmt.Errorf("installing %s: %w", name, err)
		}
		return nil
	}

	for name, why := range forbidden {
		reason := why
		what := name
		if err := set(name, func(goja.FunctionCall) goja.Value {
			panic(r.vm.NewTypeError("%s is not available in a script: %s", what, reason))
		}); err != nil {
			return err
		}
	}

	console, err := r.consoleAPI()
	if err != nil {
		return err
	}
	if err := set("console", console); err != nil {
		return err
	}

	crypto := r.vm.NewObject()
	// The one capability a script cannot build for itself, and the one the
	// design's own lib/idempotency.js needs.
	if err := crypto.Set("randomUUID", resolve.NewUUID); err != nil {
		return err
	}
	if err := set("crypto", crypto); err != nil {
		return err
	}

	varsAPI, err := r.varsAPI()
	if err != nil {
		return err
	}
	if err := set("vars", varsAPI); err != nil {
		return err
	}

	secretsAPI, err := r.secretsAPI()
	if err != nil {
		return err
	}
	if err := set("secrets", secretsAPI); err != nil {
		return err
	}

	// request is available in both phases: pre-request to shape it,
	// post-response to read what was sent. response only exists afterwards.
	if r.opts.Request != nil {
		view, err := r.requestAPI()
		if err != nil {
			return err
		}
		if err := set("request", view); err != nil {
			return err
		}
	}
	if r.opts.Phase == Post && r.opts.Response != nil {
		view, err := r.responseAPI()
		if err != nil {
			return err
		}
		if err := set("response", view); err != nil {
			return err
		}
	}

	// test and expect are post-response only: a test is an assertion about a
	// response, and one in a pre-request hook would be asserting on nothing.
	if r.opts.Phase == Post {
		if err := set("test", r.testFn); err != nil {
			return err
		}
		if err := set("expect", r.expectFn); err != nil {
			return err
		}
	} else {
		for _, name := range []string{"test", "expect"} {
			what := name
			if err := set(name, func(goja.FunctionCall) goja.Value {
				panic(r.vm.NewTypeError(
					"%s is only available in a post-response script: a test asserts on a response", what))
			}); err != nil {
				return err
			}
		}
	}

	// The module hook the import transform compiles down to (§9.8).
	return set(importFn, r.importModule)
}

// ConsoleLine is one captured console call.
type ConsoleLine struct {
	// Level is "log", "info", "warn", "error" or "debug".
	Level string `json:"level"`
	// Text is the arguments joined by a space, already masked.
	Text string `json:"text"`
	// Path is the script the call came from.
	Path string    `json:"path,omitempty"`
	At   time.Time `json:"at"`
}

// consoleAPI installs `console`, capturing every call.
//
// Captured rather than printed: a script's output belongs in the window beside
// the response it explains, not in a log file on the machine. It is masked on
// the way in, so a handle that reaches a console call is a mask in the record
// and not only on screen.
func (r *Runtime) consoleAPI() (*goja.Object, error) {
	console := r.vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		at := level
		if err := console.Set(at, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, arg := range call.Arguments {
				text, _ := r.text(arg)
				parts = append(parts, text)
			}
			line := ConsoleLine{Level: at, Text: r.maskText(join(parts, " ")), At: time.Now()}
			r.mu.Lock()
			r.console = append(r.console, line)
			r.mu.Unlock()
			if r.opts.OnConsole != nil {
				r.opts.OnConsole(line)
			}
			return goja.Undefined()
		}); err != nil {
			return nil, err
		}
	}
	return console, nil
}

// join is strings.Join without the import cycle of taste that would come from
// reaching for a helper file for one call.
func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
