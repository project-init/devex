package components

import (
	"fmt"
	"path/filepath"

	"github.com/project-init/devex/internal/components/db"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:           "components <configuration_file_path>",
		Short:         "components generates component files based on configuration",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Using Configuration - %+v\n", cfg)

			return outputDBFiles(cfg)
		},
	}
}

func outputDBFiles(cfg *Config) error {
	if cfg.DB.SchemaName == "" {
		return nil
	}

	return db.OutputFiles(cfg.DB, filepath.Join(cfg.OutputDirectory, "db"), "")
}
