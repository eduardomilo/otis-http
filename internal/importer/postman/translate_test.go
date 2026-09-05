package postman

import (
	"strings"
	"testing"
)

// The shape almost every real Postman script has: read the response, chain an
// id. Measured across three real exports — 43 scripts, 314 non-blank lines —
// the whole `pm.` surface in use was five identifiers, and 40 of the 43
// translated completely.
func TestTranslateTheShapeRealScriptsHave(t *testing.T) {
	got := Translate(`const res = pm.response.json();
pm.collectionVariables.set("claId", res.id);
postman.setEnvironmentVariable("offerId", res.offers[0].id);
console.log("claId", res.id);`)

	if !got.Complete {
		t.Fatalf("blocked by %+v", got.Blockers)
	}
	want := `const res = response.json();
vars.session.set("claId", res.id);
vars.session.set("offerId", res.offers[0].id);
console.log("claId", res.id);`
	if got.Code != want {
		t.Errorf("got:\n%s\nwant:\n%s", got.Code, want)
	}
	if got.Changed["vars.session.set"] != 2 || got.Changed["response.json"] != 1 {
		t.Errorf("changed = %v", got.Changed)
	}
}

func TestTranslateEachRule(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		// Every Postman setter becomes the one Otis scope that writes no
		// file. See translate.go for why it is never vars.env.set.
		{`pm.environment.set("a", 1)`, `vars.session.set("a", 1)`},
		{`pm.globals.set("a", 1)`, `vars.session.set("a", 1)`},
		{`pm.variables.set("a", 1)`, `vars.session.set("a", 1)`},
		{`pm.collectionVariables.set("a", 1)`, `vars.session.set("a", 1)`},
		{`postman.setEnvironmentVariable("a", 1)`, `vars.session.set("a", 1)`},
		{`postman.setGlobalVariable("a", 1)`, `vars.session.set("a", 1)`},

		// Reading resolves through the whole chain in both products.
		{`pm.environment.get("a")`, `vars.get("a")`},
		{`pm.variables.get("a")`, `vars.get("a")`},
		{`postman.getEnvironmentVariable("a")`, `vars.get("a")`},

		{`pm.response.json().id`, `response.json().id`},
		{`pm.response.code`, `response.status`},
		{`pm.response.status`, `response.statusText`},
		{`pm.response.text()`, `response.body`},
		{`pm.response.responseTime`, `response.timings.total`},
		{`pm.response.responseSize`, `response.size`},
		{`pm.response.headers.get("etag")`, `response.headers.get("etag")`},

		{`pm.request.method`, `request.method`},
		{`pm.request.url.toString()`, `request.url`},
		{`pm.info.requestName`, `request.name`},

		{`pm.test("ok", fn)`, `test("ok", fn)`},

		// The pre-`pm` globals, still all over older collections.
		{`JSON.parse(responseBody).id`, `JSON.parse(response.body).id`},
		{`responseCode.code === 200`, `response.status === 200`},
		{`responseCode.name`, `response.statusText`},
	} {
		got := Translate(tt.in)
		if got.Code != tt.want {
			t.Errorf("Translate(%q) = %q, want %q", tt.in, got.Code, tt.want)
		}
		if !got.Complete {
			t.Errorf("Translate(%q) was blocked by %+v", tt.in, got.Blockers)
		}
	}
}

// The rule that keeps the whole feature honest: a substitution is made only
// where the result is exactly right.
//
// `pm.expect` is the case that matters. Renaming it to `expect` would produce
// code that looks translated, passes every textual check, and throws at run
// time — Otis' expect has `.toEqual` and no chai chain (docs/FORMAT.md §9.9).
// A plausible-but-wrong rename is worse than none, because completeness is
// what decides whether the file is allowed to run.
func TestTranslateRefusesAPlausibleButWrongRename(t *testing.T) {
	got := Translate(`pm.test("ok", () => pm.expect(pm.response.code).to.eql(200));`)
	if got.Complete {
		t.Fatal("chai was accepted as translated")
	}
	// The safe half was still done.
	if !strings.Contains(got.Code, `test("ok"`) || !strings.Contains(got.Code, "response.status") {
		t.Errorf("the safe substitutions were not made: %s", got.Code)
	}
	// And the unsafe half was left exactly as it was, for a person to read.
	if !strings.Contains(got.Code, "pm.expect") {
		t.Errorf("pm.expect was rewritten: %s", got.Code)
	}
	if got.Blockers[0].What != "pm.expect" || !strings.Contains(got.Blockers[0].Why, "chai") {
		t.Errorf("blocker = %+v", got.Blockers[0])
	}
}

func TestTranslateBlocks(t *testing.T) {
	for _, tt := range []struct{ in, what string }{
		{`const moment = require('moment');`, "require("},
		{`setTimeout(() => {}, 100)`, "setTimeout"},
		{`setInterval(f, 1)`, "setTimeout"},
		{`pm.sendRequest("https://x", cb)`, "pm.sendRequest"},
		{`eval(src)`, "eval("},
		{`tests["ok"] = true;`, "tests["},
		{`pm.request.headers.add({key: "A", value: "b"})`, "pm.request.headers"},
		{`pm.iterationData.get("row")`, "pm.iterationData"},
		{`pm.cookies.get("sid")`, "pm.cookies"},
		{`const h = CryptoJS.HmacSHA256(a, b);`, "a bundled library"},
		{`_.each(xs, f)`, "a bundled library"},
		// The catch-all: API this table does not know about must not slip
		// through, or a file that throws would be allowed to run.
		{`pm.variables.replaceIn("{{$guid}}")`, "pm."},
	} {
		got := Translate(tt.in)
		if got.Complete {
			t.Errorf("Translate(%q) reported complete", tt.in)
			continue
		}
		if got.Blockers[0].What != tt.what {
			t.Errorf("Translate(%q) blocked on %q, want %q", tt.in, got.Blockers[0].What, tt.what)
		}
		if got.Blockers[0].Why == "" {
			t.Errorf("Translate(%q) gave no reason", tt.in)
		}
	}
}

// A commented-out `pm.` call is not code, and blocking on one would keep a
// perfectly translated script from running. Postman scripts are full of them.
func TestTranslateIgnoresComments(t *testing.T) {
	got := Translate(`// old: pm.expect(x).to.eql(y);
//   const moment = require('moment');
pm.collectionVariables.set("id", pm.response.json().id);`)
	if !got.Complete {
		t.Fatalf("a comment blocked the file: %+v", got.Blockers)
	}
	// The comment's own text is left alone — it is prose about what used to
	// be there, and rewriting it would make it a lie.
	if !strings.Contains(got.Code, "// old: pm.expect(x).to.eql(y);") {
		t.Errorf("a comment was rewritten: %s", got.Code)
	}
}

// Blockers carry the line, because "something in this file needs you" is not
// an instruction and "line 3 needs you" is.
func TestTranslateReportsTheLine(t *testing.T) {
	got := Translate(`const id = pm.response.json().id;
pm.collectionVariables.set("id", id);
const moment = require('moment');`)
	if len(got.Blockers) != 1 {
		t.Fatalf("blockers = %+v", got.Blockers)
	}
	if got.Blockers[0].Line != 3 {
		t.Errorf("line = %d, want 3", got.Blockers[0].Line)
	}
}

// One reason per line: the first rule to match is the most specific, and
// three overlapping explanations for one line is noise.
func TestTranslateGivesOneReasonPerLine(t *testing.T) {
	got := Translate(`pm.expect(pm.cookies.get("a")).to.eql(pm.iterationData.get("b"));`)
	if len(got.Blockers) != 1 {
		t.Errorf("blockers = %+v", got.Blockers)
	}
}

func TestTranslateLeavesUntouchedCodeAlone(t *testing.T) {
	const src = `const total = items.reduce((a, b) => a + b.price, 0);
if (total > 100) { console.log("big", total); }`
	got := Translate(src)
	if !got.Complete || got.Code != src {
		t.Errorf("plain JavaScript was changed: %q", got.Code)
	}
	if len(got.Changed) != 0 {
		t.Errorf("changed = %v", got.Changed)
	}
}
