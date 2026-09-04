package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/otis-http/otis/internal/mcp"
)

// `otis mcp` — the command line's half of the MCP server (docs/MCP.md §2).
//
// The server itself runs in the desktop app, because everything that makes it
// safe is there: the window that asks a person for a confirmation (§6.4), the
// open collection, and the keychain. There is deliberately no `otis mcp serve`
// — a headless server would be a listener with no way to ask anyone anything,
// which is exactly the shape this design refuses.
//
// What is left for the CLI is the part the app cannot do: printing the current
// endpoint so a client can be configured. That is the other half of §14.1's
// decision, taken as recommended — the port and token change every launch,
// which is the better security posture, and this command is what makes an
// unstable endpoint cost a paste instead of a re-read of a file nobody can
// find.

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "The MCP server for agents",
		Long: "Otis can expose the open collection to an MCP client.\n\n" +
			"The server runs in the desktop app, where there is a window to ask you\n" +
			"for confirmation before anything is sent. Turn it on there; this command\n" +
			"prints the configuration a client needs to reach it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newMCPConfigCmd())
	return cmd
}

func newMCPConfigCmd() *cobra.Command {
	var pathOnly bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the MCP client configuration for the running app",
		Long: "Prints the block to paste into your MCP client's configuration.\n\n" +
			"The port is assigned by the OS and the token is minted when you enable\n" +
			"the server, so both change every time the app starts and this has to be\n" +
			"re-run. That is deliberate: a fixed port and a stored token would be a\n" +
			"worse trade than a paste.\n\n" +
			"The output contains a bearer token, so it belongs in your client's\n" +
			"configuration file and not in a shared terminal or a bug report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := mcp.DefaultEndpointPath()
			if err != nil {
				return err
			}
			if pathOnly {
				fmt.Fprintln(cmd.OutOrStdout(), path)
				return nil
			}

			endpoint, err := mcp.ReadEndpoint(path)
			if errors.Is(err, os.ErrNotExist) {
				// Not an error worth a stack of jargon: the overwhelmingly
				// likely cause is that the server is simply not on, and the
				// fix is a switch in the app.
				return fmt.Errorf("the MCP server is not running.\n\n" +
					"Open Otis and enable it, then run this again. There is no headless\n" +
					"server: a confirmation needs a window to appear in.")
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), endpoint.ClientBlock())
			return nil
		},
	}
	cmd.Flags().BoolVar(&pathOnly, "path", false,
		"Print the path of the endpoint file rather than its contents")
	return cmd
}
