package script

import (
	"fmt"
	"strings"
	"testing"
)

// The three scopes are three lifetimes, and the API names the lifetime
// (docs/FORMAT.md §9.4).
func TestVarScopesAreThreeLifetimes(t *testing.T) {
	opts := post(t, `
		vars.request.set("scratch", "one send");
		vars.session.set("orderId", "ord_1");
		vars.env.set("cursor", "abc");
	`)
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	store := opts.Vars.(*mapStore)
	for _, c := range []struct {
		scope      VarScope
		name, want string
	}{
		{ScopeRequest, "scratch", "one send"},
		{ScopeSession, "orderId", "ord_1"},
		{ScopeEnv, "cursor", "abc"},
	} {
		if got, ok := store.ReadScope(c.scope, c.name); !ok || got != c.want {
			t.Errorf("vars.%s.get(%q) = %q, %v; want %q", c.scope, c.name, got, ok, c.want)
		}
	}
}

// `vars.folder` does not exist, and that is the point: `_folder.http` declares
// committed variables with `@name = value`, so a call named vars.folder.set
// would read as setting one of those. It sets a value that is in no file.
func TestThereIsNoVarsFolder(t *testing.T) {
	result, err := Run(post(t, `console.log(typeof vars.folder, typeof vars.session);`))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "undefined object" {
		t.Errorf("console = %q, want vars.folder absent and vars.session present", got)
	}
}

// vars.get resolves with the full fall-through; vars.<scope>.get reads one
// scope. A script and a {{reference}} must never disagree about a name.
func TestVarsGetFallsThroughAndScopeGetDoesNot(t *testing.T) {
	opts := post(t, `
		console.log("resolve", vars.get("host"));
		console.log("scoped", vars.env.get("host"), vars.request.get("host"));
		console.log("missing", vars.get("nope"));
	`)
	store := opts.Vars.(*mapStore)
	if err := store.WriteScope(ScopeEnv, "host", "api.example.test"); err != nil {
		t.Fatal(err)
	}
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"resolve api.example.test",
		"scoped api.example.test undefined",
		"missing undefined",
	}
	for i, line := range want {
		if result.Console[i].Text != line {
			t.Errorf("console[%d] = %q, want %q", i, result.Console[i].Text, line)
		}
	}
}

// The shapes that are almost always a bug are errors rather than the strings
// "undefined" and "null": a header reading `undefined` is a worse outcome
// than a message naming the call that produced it.
func TestVarSetRefusesValuesThatAreAlmostAlwaysABug(t *testing.T) {
	cases := map[string]string{
		"undefined":  `vars.request.set("a", undefined);`,
		"null":       `vars.request.set("a", null);`,
		"a function": `vars.request.set("a", () => 1);`,
		"missing":    `vars.request.set("a");`,
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			opts := post(t, code)
			if _, err := Run(opts); err == nil {
				t.Error("should be refused")
			}
			if _, ok := opts.Vars.(*mapStore).ReadScope(ScopeRequest, "a"); ok {
				t.Error("it was stored anyway")
			}
		})
	}
}

// An object is stored as JSON rather than "[object Object]", which is what
// somebody storing a body fragment actually meant.
func TestVarSetStoresAnObjectAsJSON(t *testing.T) {
	opts := post(t, `vars.request.set("item", {sku: "A", qty: 2}); vars.request.set("list", [1, 2]);`)
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	store := opts.Vars.(*mapStore)
	if got, _ := store.ReadScope(ScopeRequest, "item"); got != `{"qty":2,"sku":"A"}` && got != `{"sku":"A","qty":2}` {
		t.Errorf("item = %q, want JSON", got)
	}
	if got, _ := store.ReadScope(ScopeRequest, "list"); got != "[1,2]" {
		t.Errorf("list = %q, want JSON", got)
	}
}

// test(name, fn) records a pass or a failure and streams as it goes
// (docs/FORMAT.md §9.9).
func TestTestsRunAndStream(t *testing.T) {
	var streamed []TestResult
	opts := post(t, `
		test("201 created", () => expect(response.status).toBe(201));
		test("two line items", () => expect(response.json().line_items).toHaveLength(2));
		test("this one fails", () => expect(response.status).toBe(500));
	`)
	opts.OnTest = func(result TestResult) { streamed = append(streamed, result) }

	result, err := Run(opts)
	if err != nil {
		t.Fatalf("a failing test must not fail the phase: %v", err)
	}
	if len(result.Tests) != 3 {
		t.Fatalf("tests = %+v", result.Tests)
	}
	if result.Passed() != 2 || result.Failed() != 1 {
		t.Errorf("%d passed, %d failed; want 2 and 1", result.Passed(), result.Failed())
	}
	if !result.Tests[0].Passed || !result.Tests[1].Passed || result.Tests[2].Passed {
		t.Errorf("verdicts = %+v", result.Tests)
	}
	// The failure says what was expected and what arrived.
	message := result.Tests[2].Message
	if !strings.Contains(message, "500") || !strings.Contains(message, "201") {
		t.Errorf("failure = %q, want both values", message)
	}
	// Indexes let a streamed result fill in a row the window already drew.
	for i, test := range result.Tests {
		if test.Index != i {
			t.Errorf("tests[%d].Index = %d", i, test.Index)
		}
	}
	if len(streamed) != 3 {
		t.Errorf("streamed %d results, want 3 as they completed", len(streamed))
	}
}

// A test that throws for a reason other than an assertion is still a failure
// carrying its message, not a crash.
func TestTestCatchesAnyThrow(t *testing.T) {
	result, err := Run(post(t, `
		test("bad code", () => { nope(); });
		test("still runs", () => expect(1).toBe(1));
	`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tests) != 2 || result.Tests[0].Passed || !result.Tests[1].Passed {
		t.Fatalf("tests = %+v", result.Tests)
	}
	if !strings.Contains(result.Tests[0].Message, "nope") {
		t.Errorf("message = %q", result.Tests[0].Message)
	}
}

func TestTestRefusesANonFunction(t *testing.T) {
	if _, err := Run(post(t, `test("x", "not a function");`)); err == nil {
		t.Error("test() with a non-function should be refused")
	}
}

// The matcher set of §9.9, each one both ways.
func TestMatchers(t *testing.T) {
	cases := []struct {
		expr   string
		passes bool
	}{
		{`expect(1).toBe(1)`, true},
		{`expect(1).toBe(2)`, false},
		{`expect("a").toBe("a")`, true},
		{`expect({a: 1}).toEqual({a: 1})`, true},
		{`expect({a: 1}).toEqual({a: 2})`, false},
		{`expect([1, 2]).toEqual([1, 2])`, true},
		{`expect(1).toBeTruthy()`, true},
		{`expect(0).toBeTruthy()`, false},
		{`expect("").toBeFalsy()`, true},
		{`expect(1).toBeDefined()`, true},
		{`expect(undefined).toBeDefined()`, false},
		{`expect(undefined).toBeUndefined()`, true},
		{`expect(null).toBeNull()`, true},
		{`expect(1).toBeNull()`, false},
		{`expect("abcd").toContain("bc")`, true},
		{`expect("abcd").toContain("zz")`, false},
		{`expect([1, 2, 3]).toContain(2)`, true},
		{`expect([1, 2, 3]).toContain(9)`, false},
		{`expect([1, 2]).toHaveLength(2)`, true},
		{`expect([1, 2]).toHaveLength(3)`, false},
		{`expect("abc").toHaveLength(3)`, true},
		{`expect("ord_1").toMatch(/^ord_/)`, true},
		{`expect("x").toMatch(/^ord_/)`, false},
		{`expect(5).toBeGreaterThan(1)`, true},
		{`expect(1).toBeGreaterThan(5)`, false},
		{`expect(1).toBeLessThan(5)`, true},
		{`expect(5).toBeLessThan(1)`, false},

		// .not inverts every one of them, which is why there is one
		// implementation of each matcher rather than two.
		{`expect(1).not.toBe(2)`, true},
		{`expect(1).not.toBe(1)`, false},
		{`expect("abcd").not.toContain("zz")`, true},
		{`expect([1]).not.toHaveLength(2)`, true},
		{`expect(undefined).not.toBeDefined()`, true},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			result, err := Run(post(t, fmt.Sprintf(`test("t", () => %s);`, c.expr)))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Tests) != 1 {
				t.Fatalf("tests = %+v", result.Tests)
			}
			if result.Tests[0].Passed != c.passes {
				t.Errorf("%s passed = %v, want %v (message %q)",
					c.expr, result.Tests[0].Passed, c.passes, result.Tests[0].Message)
			}
		})
	}
}

// A failure message names the matcher, what was expected and what arrived.
func TestMatcherFailureMessage(t *testing.T) {
	result, err := Run(post(t, `test("t", () => expect(response.status).toBe(500));`))
	if err != nil {
		t.Fatal(err)
	}
	message := result.Tests[0].Message
	for _, want := range []string{"expected", "201", "to be", "500"} {
		if !strings.Contains(message, want) {
			t.Errorf("message = %q, want it to contain %q", message, want)
		}
	}
}
