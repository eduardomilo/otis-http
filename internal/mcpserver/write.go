package mcpserver

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/mcp"
)

// The WRITE tools (docs/MCP.md §12).
//
// Every one of these goes through the service CLAUDE.md already names as the
// only writer of that kind of file, so an agent's write is subject to the same
// invariants a person's is: it holds the write guard, it announces itself, and
// **it does not touch `.order`**. Nothing here reimplements a write.
//
// Everything written here is unreviewed by definition, so §5 applies to it
// immediately: sending it will confirm in Otis' own window, and an unreviewed
// send that would use a secret gets §5.1's danger confirmation. That is the
// whole reason WRITE can be this permissive — the gate is on *sending*, not on
// composing.
//
// There is no rename and no delete. Creating and editing are recoverable —
// the file is in the working tree and `git checkout` undoes it — and a delete
// of something uncommitted is not. Nothing an agent does should be
// unrecoverable by the tools the person already has.

// writeAnnotations is what the write tools declare (§6.1).
func writeAnnotations(idempotent bool) []mcpgo.ToolOption {
	return []mcpgo.ToolOption{
		mcpgo.WithReadOnlyHintAnnotation(false),
		// Not destructive: these create and modify files in a git working
		// tree, which is recoverable. `update_request` replacing a file's
		// text is the closest thing here to destruction, and `git checkout`
		// answers it.
		mcpgo.WithDestructiveHintAnnotation(false),
		mcpgo.WithIdempotentHintAnnotation(idempotent),
		// A write touches only the open collection on this machine.
		mcpgo.WithOpenWorldHintAnnotation(false),
	}
}

func (s *Server) registerWrite() {
	s.register(tool("create_request",
		"Create a new request file in the open Otis collection. The file is named for a slug "+
			"of `name`, and `name` is kept verbatim as the request's display name. Use the "+
			"`path` that comes back — it may carry a `-2` you did not ask for, because Otis "+
			"resolves collisions against what is actually on disk. A request you create is "+
			"uncommitted, so sending it will need a person's confirmation in Otis' window, "+
			"and Otis will not send it at all if it would use a secret until somebody commits it.",
		writeAnnotations(false),
		mcpgo.WithString("folder", mcpgo.Required(),
			mcpgo.Description("The folder to create it in, as a collection-relative path. "+
				"Empty string for the collection root.")),
		mcpgo.WithString("name", mcpgo.Required(),
			mcpgo.Description("The request's display name, e.g. \"Create order\".")),
		mcpgo.WithString("text",
			mcpgo.Description("The whole .http file's text. Must parse. Omit for a default stub.")),
	), mcp.CapWrite, spendOnEntry, func(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
		folder := req.GetString("folder", "")
		name, err := req.RequireString("name")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		created, redactor, err := s.writer.CreateRequest(folder, name, req.GetString("text", ""))
		if err != nil {
			return nil, redactor, err
		}
		c.target = created.Path
		c.status = "created"
		return created, redactor, nil
	})

	s.register(tool("create_folder",
		"Create a new folder in the open Otis collection. It gets a `_folder.http` inside it, "+
			"because git does not track an empty directory. Use the `path` that comes back.",
		writeAnnotations(false),
		mcpgo.WithString("parent", mcpgo.Required(),
			mcpgo.Description("The folder to create it in. Empty string for the collection root.")),
		mcpgo.WithString("name", mcpgo.Required(),
			mcpgo.Description("The folder's name.")),
	), mcp.CapWrite, spendOnEntry, func(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
		parent := req.GetString("parent", "")
		name, err := req.RequireString("name")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		created, redactor, err := s.writer.CreateFolder(parent, name)
		if err != nil {
			return nil, redactor, err
		}
		c.target = created.Path
		c.status = "created"
		return created, redactor, nil
	})

	s.register(tool("update_request",
		"Replace a request file's whole text. Read `get_request`'s `source` first and edit "+
			"those bytes: this replaces the file, so anything you leave out is gone — including "+
			"comments, directives and scripts you did not recognise. Text that does not parse "+
			"is refused and the file is left alone. Your change shows up in Otis' diff view "+
			"like any other.",
		writeAnnotations(true),
		mcpgo.WithString("path", mcpgo.Required(),
			mcpgo.Description("The request's collection-relative path.")),
		mcpgo.WithString("text", mcpgo.Required(),
			mcpgo.Description("The whole .http file's new text. Must parse.")),
	), mcp.CapWrite, spendOnEntry, func(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		text, err := req.RequireString("text")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		updated, redactor, err := s.writer.UpdateRequest(path, text)
		if err != nil {
			return nil, redactor, err
		}
		c.status = "modified"
		return updated, redactor, nil
	})

	s.register(tool("update_documentation",
		"Replace a folder's README.md — its documentation for everyone on the branch, and "+
			"the collection's own when `folder` is the empty string. Read "+
			"`get_documentation` first and edit those bytes: this replaces the whole file. "+
			"Nothing parses a README, so nothing will refuse a mistake; your change is "+
			"uncommitted and shows up in Otis' diff view, which is where a person reads it. "+
			"A README changes no request and is never executed — Otis renders it as markdown "+
			"and does not follow its links.",
		writeAnnotations(true),
		mcpgo.WithString("folder", mcpgo.Required(),
			mcpgo.Description("The folder, as a collection-relative path. "+
				"Empty string for the collection root.")),
		mcpgo.WithString("text", mcpgo.Required(),
			mcpgo.Description("The whole README.md's new text.")),
	), mcp.CapWrite, spendOnEntry, func(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
		folder := req.GetString("folder", "")
		text, err := req.RequireString("text")
		if err != nil {
			return nil, mcp.NoSecrets(), err
		}
		updated, redactor, err := s.writer.UpdateDocumentation(folder, text)
		if err != nil {
			return nil, redactor, err
		}
		c.target = updated.Path
		c.status = "modified"
		return updated, redactor, nil
	})
}
