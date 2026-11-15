package components

import (
	"fmt"
	"path/filepath"

	"github.com/project-init/devex/internal/components/db"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

// generateJSON is the --output json document.
type generateJSON struct {
	DryRun bool      `json:"dryRun"`
	Files  []db.File `json:"files"`
}

// Command builds devex components.
func Command() *cobra.Command {
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "components <configuration_file_path>",
		Short: "Generate component files from a configuration file",
		Long: "Generate component files from a YAML configuration. It keeps files a team edits, such as structure.sql " +
			"and the seed data, unless you pass --force. --dry-run lists what would change and writes nothing.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		Annotations:   map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ui.New(cmd), args[0], dryRun, force)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list the files generation would write, overwrite, or skip, and write none")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the files a team edits, such as structure.sql and the seed data")
	return cmd
}

func run(u *ui.UI, configPath string, dryRun, force bool) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	dir := filepath.Join(cfg.OutputDirectory, "db")
	files, err := db.Plan(cfg.DB, dir, force)
	if err != nil {
		return err
	}
	if !dryRun {
		u.Status("Generating database components for schema %s in %s", cfg.DB.SchemaName, dir)
		if err := db.Write(files); err != nil {
			return err
		}
	}
	if u.JSON() {
		return u.WriteJSON(generateJSON{DryRun: dryRun, Files: files})
	}
	rows := make([][]string, len(files))
	counts := map[db.Action]int{}
	for index, file := range files {
		rows[index] = []string{string(file.Action), file.Path}
		counts[file.Action]++
	}
	u.Table([]string{"Action", "File"}, rows)
	summary := fmt.Sprintf("create %d, overwrite %d, skip %d", counts[db.ActionCreate], counts[db.ActionOverwrite], counts[db.ActionSkip])
	switch {
	case dryRun:
		u.Status("Dry run: would %s. Nothing written.", summary)
	case counts[db.ActionSkip] > 0:
		u.Success("Generated: %s. --force overwrites the skipped files.", summary)
	default:
		u.Success("Generated: %s.", summary)
	}
	return nil
}
