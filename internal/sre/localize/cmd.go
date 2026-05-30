package localize

import (
	"github.com/project-init/devex/internal/sre/localize/audit"
	"github.com/project-init/devex/internal/sre/localize/translate"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "localize",
		Short: "Localization Management (using Gemini)",
		Long:  "Manage translations for projects using the 'gotext' workflow. Detect missing keys and translate them using the Gemini LLM.",
	}

	cmd.AddCommand(audit.Command())
	cmd.AddCommand(translate.Command())

	return cmd
}
