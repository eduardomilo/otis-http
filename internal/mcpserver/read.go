package mcpserver

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/mcp"
)

// The READ tools (docs/MCP.md §12).
//
// None of them asks anyone anything: READ is off by default, and granting it
// is the consent. What each one does have to do is refuse to be a way around
// §7 — an environment's non-secret values are still somebody's
// infrastructure, and `get_request.source` is a file that may contain a
// credential a colleague committed.

// registerAll installs the tool surface.
//
// Grouped by capability, in the order §12 lists them, so the surface can be
// read against the document rather than assembled from a search.
func (s *Server) registerAll() {
	s.registerRead()
}

func (s *Server) registerRead() {
	s.register(tool("list_requests",
		"List the requests in the open Otis collection, as a tree. This is the same tree "+
			"the Otis sidebar draws and `otis ls` prints. Paths returned here are what every "+
			"other tool takes.",
		readOnly(),
		mcpgo.WithString("folder",
			mcpgo.Description("Limit to this folder, as a collection-relative path. Omit for the whole collection.")),
		mcpgo.WithBoolean("includeFolders",
			mcpgo.Description("Include folders as well as requests.")),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		return s.source.ListRequests(req.GetString("folder", ""), req.GetBool("includeFolders", false))
	})

	s.register(tool("get_request",
		"Read one request: the effective values it would send, with provenance for each, "+
			"and `source`, the file's own text. Use `source` when you intend to edit the "+
			"request — editing it is a read-modify-write on those bytes, not on this summary. "+
			"Values that came from a secret are masked and cannot be read. `gitStatus` tells "+
			"you whether sending this will need the person's confirmation.",
		readOnly(),
		mcpgo.WithString("path", mcpgo.Required(),
			mcpgo.Description("The request's collection-relative path, as returned by list_requests.")),
		mcpgo.WithString("environment",
			mcpgo.Description("Resolve against this environment instead of the active one. "+
				"This does not change which environment Otis is using.")),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		return s.source.GetRequest(path, req.GetString("environment", ""))
	})

	s.register(tool("list_environments",
		"List the collection's environments: their names, which variables each defines, and "+
			"which of those are secrets. Never any values — an environment's non-secret "+
			"values are still somebody's infrastructure. `confirmBeforeSend` and `agents` "+
			"tell you how sending against each will be gated.",
		readOnly(),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		return s.source.ListEnvironments()
	})

	s.register(tool("get_session_variables",
		"List the variables previous sends and runs have set in this session. A secret's "+
			"value is withheld. These are not written to any file — they are scratch state "+
			"between requests.",
		readOnly(),
		mcpgo.WithString("folder",
			mcpgo.Description("Limit to variables owned by this folder.")),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		return s.source.SessionVariables(req.GetString("folder", ""))
	})

	s.register(tool("get_last_response",
		"Read a response Otis is holding, a page of body lines at a time. Omit `sendId` for "+
			"the most recent send. The body is paged rather than returned whole; ask for the "+
			"lines you need.",
		readOnly(),
		mcpgo.WithString("sendId",
			mcpgo.Description("Which send's response. Omit for the most recent.")),
		mcpgo.WithNumber("offset", mcpgo.Min(0),
			mcpgo.Description("First body line to return, zero-based.")),
		mcpgo.WithNumber("limit", mcpgo.Min(1), mcpgo.Max(MaxBodyLines),
			mcpgo.Description("How many body lines to return. Defaults to 200.")),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		// The cap is applied here rather than trusted from the schema: a
		// schema is a description of what a well-behaved client sends, and
		// the reason a body is paged at all is that the naive version was
		// measurably too slow.
		limit := req.GetInt("limit", DefaultBodyLines)
		if limit < 1 {
			limit = DefaultBodyLines
		}
		if limit > MaxBodyLines {
			limit = MaxBodyLines
		}
		offset := req.GetInt("offset", 0)
		if offset < 0 {
			offset = 0
		}
		return s.source.LastResponse(req.GetString("sendId", ""), offset, limit)
	})

	s.register(tool("get_test_results",
		"Read the assertions a send or run declared, and whether each passed. Omit `sendId` "+
			"for the most recent send.",
		readOnly(),
		mcpgo.WithString("sendId",
			mcpgo.Description("Which send's tests. Omit for the most recent.")),
	), mcp.CapRead, func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error) {
		return s.source.TestResults(req.GetString("sendId", ""))
	})
}
