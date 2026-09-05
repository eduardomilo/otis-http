package curl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Splitting a pasted command into words, and putting one back together.
//
// This is a POSIX shell's quoting rules and nothing more — it does not
// expand variables, run substitutions or glob, and it never will: the input is
// a command somebody copied out of a browser or a terminal, not a program to
// execute. Everything here answers one question, "which characters were one
// argument", and the answer is used to fill in a `.http` file.

// splitWords tokenizes a command line the way a shell would.
//
// It handles the four quoting forms a copied `curl` actually arrives in:
// single quotes, double quotes with backslash escapes, `$'...'` ANSI-C
// quoting — which is what a browser emits for a header containing a newline —
// and a bare backslash, including the `\` at end of line that every
// multi-line curl command uses.
func splitWords(command string) ([]string, error) {
	var words []string
	var current strings.Builder
	started := false

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if started {
				words = append(words, current.String())
				current.Reset()
				started = false
			}

		case c == '\\':
			// A backslash before a newline is a line continuation and
			// disappears with it; before anything else it is that character,
			// literally.
			if i+1 < len(runes) {
				i++
				if runes[i] == '\n' || runes[i] == '\r' {
					continue
				}
				current.WriteRune(runes[i])
				started = true
			}

		case c == '\'':
			started = true
			end := indexFrom(runes, i+1, '\'')
			if end < 0 {
				return nil, fmt.Errorf("unbalanced ' in the command")
			}
			current.WriteString(string(runes[i+1 : end]))
			i = end

		case c == '"':
			started = true
			var err error
			i, err = readDoubleQuoted(runes, i, &current)
			if err != nil {
				return nil, err
			}

		case c == '$' && i+1 < len(runes) && runes[i+1] == '\'':
			started = true
			var err error
			i, err = readAnsiC(runes, i+1, &current)
			if err != nil {
				return nil, err
			}

		default:
			current.WriteRune(c)
			started = true
		}
	}
	if started {
		words = append(words, current.String())
	}
	return words, nil
}

// readDoubleQuoted consumes a "..." run starting at the opening quote and
// returns the index of the closing one.
func readDoubleQuoted(runes []rune, at int, out *strings.Builder) (int, error) {
	for i := at + 1; i < len(runes); i++ {
		switch runes[i] {
		case '"':
			return i, nil
		case '\\':
			if i+1 >= len(runes) {
				return 0, fmt.Errorf(`unbalanced " in the command`)
			}
			i++
			// Inside double quotes a backslash is only special before these;
			// anywhere else it is a literal backslash, which is why a Windows
			// path in a header value survives.
			switch runes[i] {
			case '"', '\\', '$', '`':
				out.WriteRune(runes[i])
			case '\n':
			default:
				out.WriteRune('\\')
				out.WriteRune(runes[i])
			}
		default:
			out.WriteRune(runes[i])
		}
	}
	return 0, fmt.Errorf(`unbalanced " in the command`)
}

// readAnsiC consumes a $'...' run. `at` is the index of the opening quote.
func readAnsiC(runes []rune, at int, out *strings.Builder) (int, error) {
	for i := at + 1; i < len(runes); i++ {
		switch runes[i] {
		case '\'':
			return i, nil
		case '\\':
			if i+1 >= len(runes) {
				return 0, fmt.Errorf("unbalanced $' in the command")
			}
			i++
			switch runes[i] {
			case 'n':
				out.WriteRune('\n')
			case 'r':
				out.WriteRune('\r')
			case 't':
				out.WriteRune('\t')
			case '\\', '\'', '"':
				out.WriteRune(runes[i])
			case 'x':
				value, next := readHex(runes, i+1, 2)
				out.WriteRune(rune(value))
				i = next - 1
			case 'u':
				value, next := readHex(runes, i+1, 4)
				out.WriteRune(rune(value))
				i = next - 1
			default:
				out.WriteRune(runes[i])
			}
		default:
			out.WriteRune(runes[i])
		}
	}
	return 0, fmt.Errorf("unbalanced $' in the command")
}

// readHex reads up to n hex digits from at, returning the value and the index
// after the last digit consumed.
func readHex(runes []rune, at, n int) (int64, int) {
	end := at
	for end < len(runes) && end-at < n && isHex(runes[end]) {
		end++
	}
	if end == at {
		return 0, at
	}
	value, _ := strconv.ParseInt(string(runes[at:end]), 16, 32)
	return value, end
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func indexFrom(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// quote renders one argument for a POSIX shell.
//
// Single quotes, always, and a `'` inside becomes `'\''` — the one form that
// needs no thought about what else is in the string. A value with a
// `{{variable}}` in it, a JSON body, a Windows path: all of them are literal
// inside single quotes, which is the point.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printable reports whether a body can go into a command at all. A curl
// command is text, so bytes that are not are named rather than mangled.
func printable(b []byte) bool { return utf8.Valid(b) }
