package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/otis-http/otis/internal/collection"
)

func newLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls [dir]",
		Short: "List a collection as a tree",
		Long: "List the requests and folders of a collection in display order,\n" +
			"honouring the .order file in each directory.\n\n" +
			"Broken files are listed with \"?\" in the method column; the reason is\n" +
			"printed after the tree.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			c, err := collection.Load(dir)
			if err != nil {
				return err
			}
			rows := treeRows(c.Root, 0)
			width := 0
			for _, r := range rows {
				if n := len(r.label); n > width {
					width = n
				}
			}
			out := cmd.OutOrStdout()
			for _, r := range rows {
				line := fmt.Sprintf("%-7s %s", r.method, r.label)
				if r.name != "" {
					line = fmt.Sprintf("%-7s %-*s  %s", r.method, width, r.label, r.name)
				}
				fmt.Fprintln(out, strings.TrimRight(line, " "))
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "(empty collection)")
			}
			if len(c.Warnings) > 0 {
				errw := cmd.ErrOrStderr()
				fmt.Fprintf(errw, "\n%d warning(s):\n", len(c.Warnings))
				for _, w := range c.Warnings {
					fmt.Fprintf(errw, "  %s\n", w)
				}
			}
			return nil
		},
	}
	return cmd
}

type treeRow struct {
	method string
	label  string // indented entry name
	name   string // display name, when it adds information
}

func treeRows(n *collection.Node, depth int) []treeRow {
	var rows []treeRow
	for _, ch := range n.Children {
		indent := strings.Repeat("  ", depth)
		switch ch.Kind {
		case collection.KindFolder:
			rows = append(rows, treeRow{label: indent + ch.Name + "/"})
			rows = append(rows, treeRows(ch, depth+1)...)
		default:
			base := strings.TrimSuffix(pathBase(ch.ID), collection.RequestExt)
			method := ch.Method
			if ch.Broken {
				method = "?"
			}
			name := ch.Name
			if name == base {
				name = ""
			}
			rows = append(rows, treeRow{method: method, label: indent + base + collection.RequestExt, name: name})
		}
	}
	return rows
}

func pathBase(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return id
}
