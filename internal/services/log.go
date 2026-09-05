package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/events"
)

// LogEntry is one line of the activity log.
//
// **It carries only what the window is already shown.** A message here is the
// text of an error a binding call would have returned, or the text a
// background failure would otherwise have written to stderr where nobody
// would see it. Nothing is enriched on the way in — no resolved URL, no
// header, no body — for the same reason mcp.Entry has nowhere to put a
// payload: a resolved URL can carry a credential in a query parameter, and a
// log is the one artefact that is read later, copied into a bug report and
// pasted into a chat.
type LogEntry struct {
	// ID orders the list and keys the rows. It is per-session and starts at 1.
	ID int64 `json:"id"`
	// At is when it happened, RFC3339 with milliseconds.
	At string `json:"at"`
	// Level is "error", "warn" or "info".
	Level string `json:"level"`
	// Source is which part of Otis reported it — "collection", "send",
	// "window". Short, because the row is 11px mono in a 380px popover.
	Source string `json:"source"`
	// Message is what happened, in words.
	Message string `json:"message"`
	// Detail is the underlying error, when there was one.
	Detail string `json:"detail,omitempty"`
}

// Log levels.
const (
	LogError = "error"
	LogWarn  = "warn"
	LogInfo  = "info"
)

// logCapacity is how many entries are kept. A session that logs more than
// this has a problem the oldest lines are not going to explain.
const logCapacity = 200

// LogService is the activity log: what Otis did that did not work, and where
// it now goes.
//
// Until this existed there was nowhere. A clipboard write that failed, a
// reveal that could not find the file manager, a watcher that stopped — each
// went to `console.error` in a webview with no console, or to a Wails logger
// writing to a stderr the packaged app does not have. The tree's own helper
// said so in a comment: "There is nowhere to show it yet — the design has no
// toast." This is the somewhere.
//
// It is deliberately not a toast. A toast interrupts and then vanishes, which
// is the wrong shape for something you want to look at *after* noticing that
// a thing did not work — which is how these are usually noticed. The status
// bar counts what has arrived and the popover holds the list.
//
// It is in memory and per-session. A log on disk would be a second audit
// trail with none of the care `internal/mcp`'s has (§9's 0600 file in the
// config directory, never in the collection), and nothing here is worth that.
type LogService struct {
	app *application.App

	mu      sync.Mutex
	entries []LogEntry
	next    int64
}

// NewLogService constructs the service.
func NewLogService() *LogService { return &LogService{next: 1} }

// ServiceStartup resolves the application.
func (s *LogService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// Entries returns the log, newest first.
func (s *LogService) Entries() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, 0, len(s.entries))
	for i := len(s.entries) - 1; i >= 0; i-- {
		out = append(out, s.entries[i])
	}
	return out
}

// Clear empties the log.
func (s *LogService) Clear() []LogEntry {
	s.mu.Lock()
	s.entries = nil
	s.mu.Unlock()
	s.emit(LogEntry{})
	return nil
}

// Record adds an entry and returns it.
//
// It is exported because the *window* records through it too: a binding call
// the window made and could not do anything about is exactly the kind of
// failure this exists for, and having one list rather than two is the point.
func (s *LogService) Record(level, source, message, detail string) LogEntry {
	if level == "" {
		level = LogInfo
	}
	s.mu.Lock()
	entry := LogEntry{
		ID:      s.next,
		At:      time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:   level,
		Source:  source,
		Message: message,
		Detail:  detail,
	}
	s.next++
	s.entries = append(s.entries, entry)
	if len(s.entries) > logCapacity {
		s.entries = s.entries[len(s.entries)-logCapacity:]
	}
	s.mu.Unlock()
	s.emit(entry)
	return entry
}

func (s *LogService) emit(entry LogEntry) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(events.LogAppended, entry)
}

// appLog is the process's log, set once in main.go so the services that
// report background failures can reach it without each taking it as a
// constructor argument.
//
// A package-level handle rather than a dependency, because the alternative is
// threading one service through eight constructors to carry a line of text.
// It is written once before any service runs and only read afterwards.
var appLog *LogService

// UseLog makes log the destination for every service's background failures.
func UseLog(log *LogService) { appLog = log }

// recordError sends a background failure to the Wails logger, as before, and
// to the activity log, which is the half a person can see.
func recordError(app *application.App, source, msg string, err error) {
	if app != nil {
		app.Logger.Error(msg, "error", err)
	}
	if appLog != nil {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		appLog.Record(LogError, source, msg, detail)
	}
}

// logf is recordError for a failure with no error value behind it.
func logf(app *application.App, source, format string, args ...any) {
	recordError(app, source, fmt.Sprintf(format, args...), nil)
}
