package postman

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Translating a Postman script into an Otis one.
//
// # Why this is worth doing at all
//
// Measured against three real exports — 43 scripts, 314 non-blank lines — the
// whole Postman surface they used was five identifiers: `collectionVariables.set`,
// `response.json`, `variables.set`, `setEnvironmentVariable` and `responseBody`.
// No `pm.test`, no `pm.expect`, no `pm.sendRequest`. The chai assertion
// language that would be the hard part is, in practice, not there. Forty of
// those forty-three translate completely; the three that do not all fail on
// `require('moment')`.
//
// # The rule that keeps it honest
//
// **A substitution is made only where the result is exactly right.** Anything
// needing a shape change or a different assertion language is left alone and
// becomes a blocker instead. `pm.expect(x).to.eql(y)` is the case that makes
// this matter: renaming `pm.expect` to `expect` would produce code that looks
// translated, passes every textual check, and throws at run time because
// Otis' `expect` has `.toEqual` and no chai chain (docs/FORMAT.md §9.9). A
// rename that produces plausible-but-wrong code is worse than no rename,
// because the completeness check below is what decides whether the file is
// allowed to *run*.
//
// # The one decision a table cannot make
//
// Postman's variable scopes and Otis' three lifetimes do not line up, and one
// of Otis' three — `vars.env.set` — writes `env/<active>.json`, which is
// committed. So every Postman set becomes **`vars.session.set`**, never
// `vars.env.set`: an importer that generated code rewriting a committed file
// on every send would be the same class of mistake as an imported script that
// ran and threw. Session is the recoverable direction, it is what a chained
// flow actually wants (docs/FORMAT.md §4.5), and the header says the other
// call exists.

// Translation is what Translate made of one script.
type Translation struct {
	// Code is the translated source. When Complete is false it is still the
	// translated source — the substitutions that were safe were made — and
	// the blockers are what is left.
	Code string
	// Complete reports that nothing untranslatable survived, which is what
	// makes the file safe to write as a hook that Otis will run.
	Complete bool
	// Changed counts the substitutions made, per Otis call, for the header.
	Changed map[string]int
	// Blockers are what stopped it, in source order.
	Blockers []Blocker
}

// Blocker is one thing in a script that Otis has no equivalent for.
type Blocker struct {
	// Line is 1-based, in the translated source.
	Line int
	// What is the text that matched, e.g. "require(".
	What string
	// Why is a sentence for the person who has to finish the port.
	Why string
}

// rule is one safe substitution.
type rule struct {
	pattern *regexp.Regexp
	with    string
}

// rules are applied in order. Longer forms come first, because `pm.response.text()`
// must not be reached by a rule for `pm.response`.
var rules = []rule{
	// Variables. Postman's four setters all become the one Otis scope that
	// writes no file; see the note above.
	{regexp.MustCompile(`\bpostman\.setEnvironmentVariable\b`), "vars.session.set"},
	{regexp.MustCompile(`\bpostman\.setGlobalVariable\b`), "vars.session.set"},
	{regexp.MustCompile(`\bpm\.collectionVariables\.set\b`), "vars.session.set"},
	{regexp.MustCompile(`\bpm\.environment\.set\b`), "vars.session.set"},
	{regexp.MustCompile(`\bpm\.globals\.set\b`), "vars.session.set"},
	{regexp.MustCompile(`\bpm\.variables\.set\b`), "vars.session.set"},

	// Reading resolves through the whole scope chain in both products, so
	// `vars.get` is the exact equivalent of every one of these
	// (docs/FORMAT.md §4.2).
	{regexp.MustCompile(`\bpostman\.getEnvironmentVariable\b`), "vars.get"},
	{regexp.MustCompile(`\bpm\.collectionVariables\.get\b`), "vars.get"},
	{regexp.MustCompile(`\bpm\.environment\.get\b`), "vars.get"},
	{regexp.MustCompile(`\bpm\.globals\.get\b`), "vars.get"},
	{regexp.MustCompile(`\bpm\.variables\.get\b`), "vars.get"},

	// The response. `pm.response.text()` before anything shorter.
	{regexp.MustCompile(`\bpm\.response\.text\(\)`), "response.body"},
	{regexp.MustCompile(`\bpm\.response\.json\b`), "response.json"},
	{regexp.MustCompile(`\bpm\.response\.code\b`), "response.status"},
	{regexp.MustCompile(`\bpm\.response\.status\b`), "response.statusText"},
	{regexp.MustCompile(`\bpm\.response\.responseTime\b`), "response.timings.total"},
	{regexp.MustCompile(`\bpm\.response\.responseSize\b`), "response.size"},
	{regexp.MustCompile(`\bpm\.response\.headers\.get\b`), "response.headers.get"},

	// The request. Only the members whose shape matches; `headers.add` takes
	// an object in Postman and two arguments here, so it is a blocker.
	{regexp.MustCompile(`\bpm\.request\.url\.toString\(\)`), "request.url"},
	{regexp.MustCompile(`\bpm\.request\.method\b`), "request.method"},
	{regexp.MustCompile(`\bpm\.request\.headers\.get\b`), "request.headers.get"},
	{regexp.MustCompile(`\bpm\.info\.requestName\b`), "request.name"},

	// Tests. `pm.test(name, fn)` is `test(name, fn)` exactly. `pm.expect` is
	// deliberately absent: it is chai, and Otis' expect is not.
	{regexp.MustCompile(`\bpm\.test\b`), "test"},

	// The pre-`pm` globals, which are still all over older collections.
	{regexp.MustCompile(`\bresponseCode\.code\b`), "response.status"},
	{regexp.MustCompile(`\bresponseCode\.name\b`), "response.statusText"},
	{regexp.MustCompile(`\bresponseBody\b`), "response.body"},
}

// blocker is one thing that must not survive into a file Otis will run.
type blockerRule struct {
	pattern *regexp.Regexp
	what    string
	why     string
}

// blockers are checked *after* the rules, so anything they catch is something
// no rule could translate.
//
// The first four are the sandbox's own forbidden globals (internal/script's
// `forbidden` map, docs/FORMAT.md §9.3) and could never be translated: the
// design says a script gets a JavaScript realm and nothing else. The rest are
// Postman API that Otis has no equivalent for, or that needs a human because
// the shape differs.
var blockers = []blockerRule{
	{regexp.MustCompile(`\brequire\s*\(`), "require(",
		"Postman bundles libraries (moment, lodash, crypto-js); an Otis script has no require and imports local modules instead (docs/FORMAT.md §9.8)."},
	{regexp.MustCompile(`\bsetTimeout\s*\(|\bsetInterval\s*\(`), "setTimeout",
		"a send is synchronous, so a script has no timers (docs/FORMAT.md §9.3)."},
	{regexp.MustCompile(`\bpm\.sendRequest\b`), "pm.sendRequest",
		"a script shapes the request Otis is about to send; it cannot send one of its own (docs/FORMAT.md §9.3)."},
	{regexp.MustCompile(`\beval\s*\(`), "eval(",
		"nothing in Otis needs eval, and a script that builds code at run time is a script nobody can review."},

	{regexp.MustCompile(`\bpm\.expect\b|\bpm\.response\.to\b`), "pm.expect",
		"that is chai; Otis' expect has toBe, toEqual and a handful more, with no chained `.to.have` (docs/FORMAT.md §9.9)."},
	{regexp.MustCompile(`\btests\s*\[`), "tests[",
		"the old `tests[\"name\"] = bool` form; write `test(\"name\", () => expect(bool).toBeTruthy())`."},
	{regexp.MustCompile(`\bpm\.request\.headers\.(add|upsert|remove)\b`), "pm.request.headers",
		"Postman takes an object here and Otis takes a name and a value: request.headers.add(name, value)."},
	{regexp.MustCompile(`\bpm\.iterationData\b`), "pm.iterationData",
		"a data file belongs to Postman's collection runner; Otis has no equivalent."},
	{regexp.MustCompile(`\bpm\.cookies\b`), "pm.cookies",
		"Otis keeps a cookie jar per send but does not expose it to scripts."},
	{regexp.MustCompile(`\bxml2Json\b|\bCryptoJS\b|\b_\.[A-Za-z]`), "a bundled library",
		"Postman puts lodash, CryptoJS and xml2Json in the global scope; an Otis script has only the JavaScript realm."},

	// The catch-all, last, so a more specific reason wins. Anything still
	// spelled `pm.` or `postman.` is API this table does not know about, and
	// letting it through would be letting a file run that throws.
	{regexp.MustCompile(`\bpm\.[A-Za-z]|\bpostman\.[A-Za-z]`), "pm.",
		"Otis has no equivalent for this call, or it needs a decision a table cannot make."},
}

// Translate rewrites a Postman script as far as it safely can.
//
// It never fails: a script it cannot finish comes back translated as far as it
// went, with the blockers named. What the caller does with that is the
// decision — a complete translation may be written as a hook Otis runs, and an
// incomplete one must not be.
func Translate(code string) Translation {
	out := Translation{Code: code, Changed: map[string]int{}}
	for _, r := range rules {
		hits := len(r.pattern.FindAllString(out.Code, -1))
		if hits == 0 {
			continue
		}
		out.Changed[r.with] += hits
		out.Code = r.pattern.ReplaceAllString(out.Code, r.with)
	}

	lines := strings.Split(out.Code, "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		// A comment is not code. Postman scripts are full of commented-out
		// `pm.` calls, and blocking a file because of one would keep a
		// perfectly translated script from running.
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		for _, b := range blockers {
			if !b.pattern.MatchString(line) {
				continue
			}
			key := fmt.Sprintf("%d:%s", i+1, b.what)
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Blockers = append(out.Blockers, Blocker{Line: i + 1, What: b.what, Why: b.why})
			// One reason per line: the first rule to match is the most
			// specific, and three overlapping explanations for one line is
			// noise rather than help.
			break
		}
	}
	out.Complete = len(out.Blockers) == 0
	return out
}

// summary is the header's line about what changed, sorted so a golden file is
// stable.
func (t Translation) summary() []string {
	if len(t.Changed) == 0 {
		return nil
	}
	names := make([]string, 0, len(t.Changed))
	for name := range t.Changed {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("//   %d → %s", t.Changed[name], name))
	}
	return out
}
