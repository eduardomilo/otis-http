package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/otis-http/otis/internal/importer/postman"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a collection from another tool",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newImportPostmanCmd())
	return cmd
}

func newImportPostmanCmd() *cobra.Command {
	var (
		outDir   string
		envFiles []string
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "postman <collection.json>",
		Short: "Import a Postman Collection v2.1 export",
		Long: "Convert a Postman collection export into a directory of .http files.\n\n" +
			"Secret-typed environment variables are written as keychain references;\n" +
			"their values are never imported. The report says what was skipped and why.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if outDir == "" {
				return fmt.Errorf("an output directory is required (-o)")
			}
			report, err := postman.Import(args[0], postman.Options{
				OutDir: outDir, Force: force, EnvFiles: envFiles,
			})
			if report != nil {
				fmt.Fprint(cmd.ErrOrStderr(), report.String())
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nWrote %d files to %s\n", len(report.Files), outDir)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&outDir, "out", "o", "", "output directory for the collection (required)")
	f.StringArrayVar(&envFiles, "env", nil, "Postman environment export to convert (repeatable)")
	f.BoolVar(&force, "force", false, "write into a non-empty directory")
	return cmd
}
