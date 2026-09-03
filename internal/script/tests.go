package script

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// TestResult is one test's outcome (docs/FORMAT.md §9.9).
type TestResult struct {
	// Index is the test's position in the phase, so a streamed result can
	// fill in a row the window already drew.
	Index int    `json:"index"`
	Name  string `json:"name"`
	// Passed is false when the function threw.
	Passed bool `json:"passed"`
	// Message is the failure, already masked. Empty on a pass.
	Message string `json:"message,omitempty"`
	// Path is the script the test was declared in.
	Path       string  `json:"path,omitempty"`
	DurationMs float64 `json:"durationMs"`
}

// testFn is `test(name, fn)`.
//
// The function runs immediately and synchronously: a test is an assertion
// about a response that has already arrived, so there is nothing to wait for
// and nothing to schedule. A throw is a failure carrying its message; a
// return is a pass.
func (r *Runtime) testFn(call goja.FunctionCall) goja.Value {
	name, _ := r.text(call.Argument(0))
	fn, ok := goja.AssertFunction(call.Argument(1))
	if !ok {
		panic(r.vm.NewTypeError("test(%q): the second argument must be a function", name))
	}

	r.mu.Lock()
	index := len(r.tests)
	r.mu.Unlock()

	started := time.Now()
	result := TestResult{Index: index, Name: name, Passed: true}

	if _, err := fn(goja.Undefined()); err != nil {
		result.Passed = false
		result.Message = r.testMessage(err)
	}
	result.DurationMs = float64(time.Since(started).Microseconds()) / 1000

	r.mu.Lock()
	r.tests = append(r.tests, result)
	r.mu.Unlock()

	// Streamed as it finishes, so a long suite fills in rather than arriving
	// all at once at the end (§9.9).
	if r.opts.OnTest != nil {
		r.opts.OnTest(result)
	}
	return goja.Undefined()
}

// testMessage renders a failure, masked.
//
// A bare "Error: " prefix is dropped: an assertion's message is a sentence
// about the values, and "Error: expected 201 to be 500" spends its first word
// telling the reader something they can see from the ✕ beside it. A named
// error keeps its name, because "TypeError: …" says something the message
// does not.
func (r *Runtime) testMessage(err error) string {
	var thrown *goja.Exception
	text := err.Error()
	if ok := asException(err, &thrown); ok {
		text = thrown.Value().String()
	}
	text = strings.TrimPrefix(text, "Error: ")
	return r.maskText(text)
}

// asException is errors.As for a goja exception, kept local so tests.go does
// not need the errors import for one call.
func asException(err error, target **goja.Exception) bool {
	for err != nil {
		if e, ok := err.(*goja.Exception); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// expectFn is `expect(actual)`.
//
// The matcher set is deliberately small (§9.9). One that grows to cover
// everything becomes a second language to learn, and `expect(x).toBe(true)`
// around an ordinary JavaScript expression covers whatever is left.
func (r *Runtime) expectFn(call goja.FunctionCall) goja.Value {
	return r.matchers(call.Argument(0), false)
}

// matchers builds the object expect() returns. negated flips every verdict,
// which is what `.not` is.
func (r *Runtime) matchers(actual goja.Value, negated bool) goja.Value {
	obj := r.vm.NewObject()

	// describe renders a value for a failure message, masked. A handle
	// yields its mask here like everywhere else.
	describe := func(v goja.Value) string {
		if h := r.handleOf(v); h != nil {
			return h.masked()
		}
		if v == nil || goja.IsUndefined(v) {
			return "undefined"
		}
		if goja.IsNull(v) {
			return "null"
		}
		if obj, ok := v.(*goja.Object); ok {
			if data, err := obj.MarshalJSON(); err == nil {
				return string(data)
			}
		}
		if v.ExportType() != nil && v.ExportType().Kind() == reflect.String {
			return fmt.Sprintf("%q", v.String())
		}
		return v.String()
	}

	// verdict throws when the matcher did not hold, taking negation into
	// account, and writes the message the reader needs: what was expected and
	// what arrived, and nothing else.
	//
	// A JavaScript Error rather than a Go one, so the message reads as
	// "expected 201 to be 500" instead of "GoError: expected …" — the reader
	// of a failing assertion does not care which language threw it.
	verdict := func(passed bool, matcher, expected, aside string) {
		if passed != negated {
			return
		}
		not := ""
		if negated {
			not = "not "
		}
		message := fmt.Sprintf("expected %s %sto %s", describe(actual), not, matcher)
		if expected != "" {
			message += " " + expected
		}
		if aside != "" {
			message += " (" + aside + ")"
		}
		panic(r.throw(r.maskText(message)))
	}

	must := func(err error) {
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
	}

	must(obj.Set("toBe", func(expected goja.Value) {
		verdict(strictEquals(actual, expected), "be", describe(expected), "")
	}))
	must(obj.Set("toEqual", func(expected goja.Value) {
		verdict(deepEquals(r.vm, actual, expected), "equal", describe(expected), "")
	}))
	must(obj.Set("toBeTruthy", func() {
		verdict(truthy(actual), "be truthy", "", "")
	}))
	must(obj.Set("toBeFalsy", func() {
		verdict(!truthy(actual), "be falsy", "", "")
	}))
	must(obj.Set("toBeDefined", func() {
		verdict(actual != nil && !goja.IsUndefined(actual), "be defined", "", "")
	}))
	must(obj.Set("toBeUndefined", func() {
		verdict(actual == nil || goja.IsUndefined(actual), "be undefined", "", "")
	}))
	must(obj.Set("toBeNull", func() {
		verdict(actual != nil && goja.IsNull(actual), "be null", "", "")
	}))
	must(obj.Set("toContain", func(item goja.Value) {
		verdict(contains(r, actual, item), "contain", describe(item), "")
	}))
	must(obj.Set("toHaveLength", func(n int64) {
		got, ok := lengthOf(r.vm, actual)
		verdict(ok && got == n, "have length", fmt.Sprintf("%d", n), "it has "+lengthText(got, ok))
	}))
	must(obj.Set("toMatch", func(pattern goja.Value) {
		verdict(matches(r, actual, pattern), "match", describe(pattern), "")
	}))
	must(obj.Set("toBeGreaterThan", func(n float64) {
		value, ok := numberOf(actual)
		verdict(ok && value > n, "be greater than", fmt.Sprintf("%g", n), notANumber(ok))
	}))
	must(obj.Set("toBeLessThan", func(n float64) {
		value, ok := numberOf(actual)
		verdict(ok && value < n, "be less than", fmt.Sprintf("%g", n), notANumber(ok))
	}))

	if !negated {
		// .not is the same object with every verdict flipped, so there is one
		// implementation of each matcher rather than two.
		must(obj.Set("not", r.matchers(actual, true)))
	}
	return obj
}

// throw builds a JavaScript Error carrying message, so a failing assertion
// reads as its own sentence rather than as a Go error that leaked out.
func (r *Runtime) throw(message string) *goja.Object {
	ctor, ok := goja.AssertConstructor(r.vm.Get("Error"))
	if !ok {
		return r.vm.NewGoError(errorf("%s", message))
	}
	err, buildErr := ctor(nil, r.vm.ToValue(message))
	if buildErr != nil {
		return r.vm.NewGoError(errorf("%s", message))
	}
	return err
}

// notANumber explains a numeric matcher that was handed something else.
func notANumber(ok bool) string {
	if ok {
		return ""
	}
	return "it is not a number"
}

func lengthText(n int64, ok bool) string {
	if !ok {
		return "no length"
	}
	return fmt.Sprintf("%d", n)
}

func strictEquals(a, b goja.Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.StrictEquals(b)
}

func deepEquals(vm *goja.Runtime, a, b goja.Value) bool {
	if strictEquals(a, b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Export to Go and compare structurally, which is what "deeply equal"
	// means for the JSON-shaped values a response produces.
	return reflect.DeepEqual(a.Export(), b.Export())
}

func truthy(v goja.Value) bool {
	if v == nil {
		return false
	}
	return v.ToBoolean()
}

func contains(r *Runtime, haystack, needle goja.Value) bool {
	if haystack == nil {
		return false
	}
	if obj, ok := haystack.(*goja.Object); ok && obj.ClassName() == "Array" {
		length, _ := lengthOf(r.vm, haystack)
		for i := int64(0); i < length; i++ {
			if strictEquals(obj.Get(fmt.Sprintf("%d", i)), needle) {
				return true
			}
		}
		return false
	}
	text, _ := r.text(haystack)
	want, _ := r.text(needle)
	return strings.Contains(text, want)
}

func lengthOf(vm *goja.Runtime, v goja.Value) (int64, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0, false
	}
	if obj, ok := v.(*goja.Object); ok {
		length := obj.Get("length")
		if length == nil || goja.IsUndefined(length) {
			return 0, false
		}
		return length.ToInteger(), true
	}
	if v.ExportType() != nil && v.ExportType().Kind() == reflect.String {
		return int64(len([]rune(v.String()))), true
	}
	return 0, false
}

func matches(r *Runtime, actual, pattern goja.Value) bool {
	text, _ := r.text(actual)
	// A RegExp has a test method; a string is compiled as one.
	if obj, ok := pattern.(*goja.Object); ok {
		if test, ok := goja.AssertFunction(obj.Get("test")); ok {
			out, err := test(obj, r.vm.ToValue(text))
			return err == nil && out.ToBoolean()
		}
	}
	want, _ := r.text(pattern)
	return strings.Contains(text, want)
}

func numberOf(v goja.Value) (float64, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0, false
	}
	number := v.ToFloat()
	if number != number { // NaN
		return 0, false
	}
	return number, true
}

// sprintf is fmt.Sprintf, named locally so vars.go can use it without
// importing fmt for one call.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
