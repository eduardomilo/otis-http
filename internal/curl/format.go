// Package curl converts between a `curl` command and an Otis request.
//
// Both directions, in one package, because they are one body of knowledge:
// which flag means which part of a request. Splitting them would mean two
// places to update when a flag's meaning is worked out, and the pair is what
// makes either one testable — a command that survives `Parse` then `Format`
// is a command whose meaning was understood.
//
// It knows nothing about collections, secrets or the filesystem. What to do
// with the result — where to write it, what to mask — belongs to the caller.
package curl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/otis-http/otis/internal/httpclient"
)

// Format renders a prepared request as a `curl` command.
//
// The input is what the sender is about to put on the wire
// (`httpclient.Request`), not the `.http` file, and that is the whole point:
// a curl command that reproduced the *file* would reproduce `{{baseUrl}}` and
// none of the inherited headers. What this copies is the request as it will
// actually happen, which is the thing worth pasting into a terminal, a bug
// report or a colleague's message.
//
// **It does not mask.** Masking is the caller's decision because only the
// caller knows which of the two things was asked for — a runnable command, or
// one that is safe to paste somewhere else — and `resolve.Resolved.Mask` is
// how the second is made.
func Format(req *httpclient.Request) string {
	var parts []string
	parts = append(parts, "curl")

	// -X is left off where curl would do the same thing without it: a GET
	// with no body. Everywhere else it is explicit, because a reader should
	// not have to know that curl infers POST from --data.
	if !(strings.EqualFold(req.Method, "GET") && len(req.Body) == 0) {
		parts = append(parts, "-X "+strings.ToUpper(req.Method))
	}

	parts = append(parts, quote(req.URL))

	for _, h := range req.Headers {
		parts = append(parts, "-H "+quote(h.Name+": "+h.Value))
	}

	// Otis follows redirects and curl does not, so reproducing the send means
	// asking for it. `@no-redirect` is the case where curl's own default is
	// already right and nothing is added.
	if !req.Options.NoRedirect {
		parts = append(parts, "-L")
	}
	if req.Options.Timeout > 0 {
		seconds := req.Options.Timeout.Seconds()
		parts = append(parts, "--max-time "+strconv.FormatFloat(seconds, 'f', -1, 64))
	}

	var trailing string
	switch {
	case len(req.Body) == 0:
	case printable(req.Body):
		// --data-raw and not -d: `-d` treats a leading @ as a file name, and
		// a JSON body starting with one is not impossible.
		parts = append(parts, "--data-raw "+quote(string(req.Body)))
	default:
		// A curl command is text. Naming what was left out beats emitting a
		// command that silently sends something else.
		trailing = fmt.Sprintf("\n# body omitted: %d bytes that are not text", len(req.Body))
	}

	return strings.Join(parts, " \\\n  ") + trailing
}
