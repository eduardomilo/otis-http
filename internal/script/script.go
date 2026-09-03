// Package script runs a collection's scripts: the `< {% %}` and `> {% %}`
// blocks of docs/FORMAT.md §1.10 and the `_pre.js` / `_post.js` files of §2.4.
//
// The API is docs/FORMAT.md §9 and this package is the implementation of that
// document. Where the two disagree the document wins; a test asserts the
// surface matches.
//
// # Why goja
//
// It is a JavaScript interpreter in pure Go, so CGO_ENABLED=0 still builds and
// a script cannot reach anything the host did not hand it. That second part is
// the sandbox: there is no filesystem, no process and no network in a goja
// realm unless something puts them there, and this package puts in exactly
// what §9.3 lists.
package script

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// DefaultTimeout is how long one phase may run before it is killed
// (docs/FORMAT.md §9.3). Overridden per request with @script-timeout.
const DefaultTimeout = 5 * time.Second

// TimeoutDirective is the directive that overrides DefaultTimeout.
const TimeoutDirective = "script-timeout"

// Phase is which half of a send a script belongs to.
type Phase string

const (
	// Pre is a pre-request script: it runs before variable resolution.
	Pre Phase = "pre-request"
	// Post is a post-response script.
	Post Phase = "post-response"
)

// Source is one script to run, with the name errors will use.
type Source struct {
	// Path is what an error names: "orders/_pre.js", or
	// "orders/create-order.http" for an inline block.
	Path string
	// Line is the 1-based line the block starts at, so an error in an inline
	// block reports the line in the .http file rather than in the block.
	Line int
	Code string
}

// Options is everything a run needs from the collection.
type Options struct {
	Phase Phase
	// Scripts are the sources to run, already in the order of §9.1.
	Scripts []Source

	// Request is the request being sent, unresolved in the Pre phase.
	Request *RequestView
	// Response is what came back, nil in the Pre phase.
	Response *ResponseView

	// Vars is the variable store. Reads go through the resolver's own scope
	// so a script and a {{reference}} never disagree (§9.4).
	Vars VarStore

	// Secrets is the real secret store. It is read only through a handle,
	// and only when the sender prepares the request.
	Secrets secrets.Store
	// Env is the active environment, nil when none is. secrets.ref needs it
	// to know what is declared a secret.
	Env *resolve.Environment
	// Collection is the collection key secrets are stored under (§5).
	Collection string

	// Modules resolves an import specifier to a module's source.
	Modules ModuleLoader

	// Timeout bounds the phase. Zero means DefaultTimeout.
	Timeout time.Duration

	// OnTest is called as each test finishes, so results stream (§9.9).
	OnTest func(TestResult)
	// OnConsole is called for each console call, already masked.
	OnConsole func(ConsoleLine)
}

// Result is what a phase produced.
type Result struct {
	Tests   []TestResult  `json:"tests"`
	Console []ConsoleLine `json:"console"`
	// Changed reports that a pre-request script altered the request, so the
	// sender knows to take RequestView's values rather than the file's.
	Changed bool `json:"changed"`
	// Handles are the secret handles a script placed on the request, keyed
	// by where it put them. The sender substitutes the real values.
	Handles map[string]*Handle `json:"-"`
}

// Passed and Failed count the tests.
func (r Result) Passed() int {
	n := 0
	for _, t := range r.Tests {
		if t.Passed {
			n++
		}
	}
	return n
}

func (r Result) Failed() int { return len(r.Tests) - r.Passed() }

// Error is a script failure with the file and line it happened at
// (docs/FORMAT.md §9.10).
type Error struct {
	Phase Phase
	// Path and Line locate the failure, as far as the interpreter could say.
	Path string
	Line int
	Msg  string
	// Timeout marks a phase killed by the budget rather than a throw.
	Timeout bool
	// Stack is the JavaScript stack, already masked, when there is one.
	Stack string
}

func (e *Error) Error() string {
	where := e.Path
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d", e.Path, e.Line)
	}
	if where == "" {
		return fmt.Sprintf("%s script: %s", e.Phase, e.Msg)
	}
	return fmt.Sprintf("%s: %s", where, e.Msg)
}

// Runtime is one JavaScript realm, for one phase of one send.
//
// One realm per phase, so every hook in a phase shares what the last one put
// on `vars`, and nothing survives to the next send (§9.1). It is not safe for
// concurrent use and is not meant to be reused.
type Runtime struct {
	vm   *goja.Runtime
	opts Options

	// handles maps a JavaScript object back to the secret behind it. A Go
	// map rather than a property on the object, so nothing in JavaScript can
	// reach the value.
	handles map[*goja.Object]*Handle

	mu      sync.Mutex
	tests   []TestResult
	console []ConsoleLine

	// modules caches an evaluated module's exports for this run (§9.8).
	modules map[string]*goja.Object
	// loading is the import chain, for cycle detection.
	loading []string
}

// Run executes one phase and returns what it produced.
//
// A phase with no scripts is not a no-op worth optimising away: it still
// costs a realm, and the sender skips the call instead.
func Run(opts Options) (Result, error) {
	if len(opts.Scripts) == 0 {
		return Result{}, nil
	}
	r := &Runtime{
		vm:      goja.New(),
		opts:    opts,
		handles: map[*goja.Object]*Handle{},
		modules: map[string]*goja.Object{},
	}
	if err := r.install(); err != nil {
		return Result{}, &Error{Phase: opts.Phase, Msg: err.Error()}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// The hard kill. goja checks for an interrupt between statements, so a
	// `while (true) {}` stops here rather than hanging the send forever.
	stop := time.AfterFunc(timeout, func() {
		r.vm.Interrupt(fmt.Sprintf("the %s scripts ran longer than %s", opts.Phase, timeout))
	})
	defer stop.Stop()

	for _, src := range opts.Scripts {
		if err := r.exec(src); err != nil {
			return r.result(), r.scriptError(src, err, timeout)
		}
	}
	return r.result(), nil
}

// result collects what the run produced.
func (r *Runtime) result() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Result{Tests: r.tests, Console: r.console, Handles: map[string]*Handle{}}
	if r.opts.Request != nil {
		out.Changed = r.opts.Request.changed
		for slot, h := range r.opts.Request.handles {
			out.Handles[slot] = h
		}
	}
	if out.Tests == nil {
		out.Tests = []TestResult{}
	}
	if out.Console == nil {
		out.Console = []ConsoleLine{}
	}
	return out
}

// exec compiles and runs one source.
//
// Each hook runs inside its own function, so its top-level declarations are
// its own: two hooks in a phase that both `import { next } from "..."` would
// otherwise collide on `const next`, and a phase is a sequence of independent
// files rather than one concatenated program. What they *do* share is the
// realm — globalThis, and everything `vars` reaches — which is what §9.1
// promises.
//
// The wrapper opens on the same line as the code's first line and closes
// after its last, so every line number a stack reports is the line number in
// the file.
func (r *Runtime) exec(src Source) error {
	code, err := transform(src)
	if err != nil {
		return err
	}
	program, err := goja.Compile(src.Path, "(function(){"+code+"\n})()", false)
	if err != nil {
		return err
	}
	_, err = r.vm.RunProgram(program)
	return err
}

// scriptError turns a goja failure into an Error with a location.
func (r *Runtime) scriptError(src Source, err error, timeout time.Duration) *Error {
	out := &Error{Phase: r.opts.Phase, Path: src.Path, Msg: err.Error()}

	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return &Error{
			Phase: r.opts.Phase, Path: src.Path, Timeout: true,
			Msg: fmt.Sprintf("killed after %s: a script cannot run longer than that (@%s)", timeout, TimeoutDirective),
		}
	}

	var thrown *goja.Exception
	if errors.As(err, &thrown) {
		// Masked: a thrown value can be anything, including something built
		// from a handle's own message.
		out.Msg = r.maskText(thrown.Value().String())
		out.Stack = r.maskText(thrown.String())
		out.Line = firstLine(thrown.String(), src)
		return out
	}

	var compileErr *goja.CompilerSyntaxError
	if errors.As(err, &compileErr) {
		out.Msg = compileErr.Error()
		return out
	}
	out.Msg = r.maskText(out.Msg)
	return out
}

// firstLine digs the line number of the failing frame out of a goja stack.
//
// An inline block's line numbers are relative to the block, so the block's
// own offset in the .http file is added back: an error in a `> {% %}` says
// which line of the request file it was.
func firstLine(stack string, src Source) int {
	for _, line := range strings.Split(stack, "\n") {
		at := strings.Index(line, src.Path+":")
		if at < 0 {
			continue
		}
		rest := line[at+len(src.Path)+1:]
		number := 0
		for _, c := range rest {
			if c < '0' || c > '9' {
				break
			}
			number = number*10 + int(c-'0')
		}
		if number == 0 {
			continue
		}
		if src.Line > 0 {
			// The block's text starts on the line after its `{%` marker.
			return src.Line + number
		}
		return number
	}
	return src.Line
}

// maskText replaces every secret value this run has handed out with its mask.
//
// The handles already stringify to the mask, so this catches only the case a
// handle cannot: text the sender built from a revealed value and handed back.
// Belt as well as braces, because the cost of missing one is the whole point
// of the package.
func (r *Runtime) maskText(text string) string {
	for _, h := range r.handles {
		if h.value != "" {
			text = strings.ReplaceAll(text, h.value, Mask(h.Name))
		}
	}
	return text
}

// TimeoutOf reads @script-timeout off a request, falling back to the default.
func TimeoutOf(entry *httpfile.Request) (time.Duration, error) {
	if entry == nil {
		return DefaultTimeout, nil
	}
	for i := len(entry.Directives) - 1; i >= 0; i-- {
		d := entry.Directives[i]
		if !strings.EqualFold(d.Name, TimeoutDirective) {
			continue
		}
		seconds, err := parseSeconds(d.Value)
		if err != nil {
			return 0, fmt.Errorf("line %d: @%s %s: %w", d.Line, TimeoutDirective, d.Value, err)
		}
		return seconds, nil
	}
	return DefaultTimeout, nil
}

// parseSeconds accepts a positive number of seconds, decimals allowed, which
// is the same rule @timeout uses (docs/FORMAT.md §1.4).
func parseSeconds(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("a timeout needs a number of seconds")
	}
	var seconds float64
	if _, err := fmt.Sscanf(value, "%g", &seconds); err != nil {
		return 0, errors.New("not a number")
	}
	if seconds <= 0 {
		return 0, errors.New("must be greater than zero")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// sortedNames is a stable listing helper the API objects use.
func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
