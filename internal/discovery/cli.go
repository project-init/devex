package discovery

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/provider"
	githubprovider "github.com/project-init/devex/internal/discovery/provider/github"
	"github.com/project-init/devex/internal/discovery/provider/jira"
	"github.com/project-init/devex/internal/discovery/publish"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/project-init/devex/internal/discovery/templates"
	"github.com/spf13/cobra"
)

const defaultConfigPath = ".sre/discovery.yaml"

func Command() *cobra.Command {
	command := &cobra.Command{
		Use:           "discovery",
		Short:         "Turn product discovery into reviewable plans and published work",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.AddCommand(initCommand())
	command.AddCommand(validateCommand())
	command.AddCommand(configCommand())
	command.AddCommand(installSkillCommand())
	command.AddCommand(publishCommand())
	return command
}

func configCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Manage project-owned discovery configuration",
	}
	command.AddCommand(initConfigCommand())
	return command
}

func initConfigCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "init [path]",
		Short: "Create an example discovery target configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := defaultConfigPath
			if len(args) == 1 {
				path = args[0]
			}
			if err := config.WriteExample(path, force); err != nil {
				return err
			}
			absolutePath, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Discovery configuration: %s\n", absolutePath)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "replace a different existing configuration file")
	return command
}

func installSkillCommand() *cobra.Command {
	var harnesses []string
	var force bool
	command := &cobra.Command{
		Use:   "install-skill [project-directory]",
		Short: "Install the discovery skill into an AI harness",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDirectory := "."
			if len(args) == 1 {
				projectDirectory = args[0]
			}
			selected, err := skill.ParseHarnesses(harnesses)
			if err != nil {
				return err
			}
			installed, err := skill.Install(projectDirectory, selected, force)
			if err != nil {
				return err
			}
			for _, path := range installed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed run-discovery skill: %s\n", path)
			}
			return nil
		},
	}
	command.Flags().StringSliceVar(&harnesses, "harness", []string{"all"}, "AI harness: codex, claude, cursor, or all")
	command.Flags().BoolVar(&force, "force", false, "replace different existing skill files")
	return command
}

func initCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init <directory> <name>",
		Short: "Create a discovery document and provider-neutral work breakdown",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			directory, err := templates.Generate(args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created discovery bundle at %s\n", directory)
			return nil
		},
	}
}

func validateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <bundle-directory>",
		Short: "Validate a discovery document and work breakdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundle, err := artifact.Load(args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Valid discovery bundle %s (%d work items, %s)\n",
				bundle.Directory,
				len(bundle.WorkBreakdown.Items),
				bundle.Digest(),
			)
			return nil
		},
	}
}

func publishCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "publish",
		Short: "Plan and apply publication to a work-tracking provider",
	}
	command.AddCommand(planCommand())
	command.AddCommand(applyCommand())
	return command
}

func planCommand() *cobra.Command {
	var configPath string
	var targetName string
	var outputPath string
	command := &cobra.Command{
		Use:   "plan <bundle-directory>",
		Short: "Create a read-only, reviewable publication plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundle, err := artifact.Load(args[0])
			if err != nil {
				return err
			}
			configuration, err := config.Load(configPath)
			if err != nil {
				return err
			}
			target, exists := configuration.Targets[targetName]
			if !exists {
				return fmt.Errorf("target %q is not defined in %s", targetName, configPath)
			}
			adapter, err := newAdapter(target, false)
			if err != nil {
				return err
			}
			plan, err := publish.CreatePlan(cmd.Context(), bundle, targetName, target, adapter)
			if err != nil {
				return err
			}
			if outputPath == "" {
				outputPath = publish.DefaultPlanPath(bundle.Directory, targetName)
			} else if !filepath.IsAbs(outputPath) {
				outputPath, err = filepath.Abs(outputPath)
				if err != nil {
					return err
				}
			}
			if err := publish.WriteYAMLAtomic(outputPath, plan); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Publication plan: %s\n", outputPath)
			for _, warning := range plan.Warnings {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning)
			}
			for _, operation := range plan.Operations {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", operation.Summary)
			}
			return nil
		},
	}
	command.Flags().StringVar(&configPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringVar(&targetName, "target", "", "named publication target")
	command.Flags().StringVar(&outputPath, "out", "", "publication plan output path")
	_ = command.MarkFlagRequired("target")
	return command
}

func applyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <plan-file>",
		Short: "Apply a frozen publication plan and write a resumable receipt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := publish.LoadPlan(args[0])
			if err != nil {
				return err
			}
			adapter, err := newAdapter(plan.Target, true)
			if err != nil {
				return err
			}
			receipt, err := publish.Apply(cmd.Context(), args[0], adapter, func(result provider.OperationResult) {
				if result.Remote != nil {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", result.Status, result.ItemID, result.Remote.URL)
				}
			})
			if receipt != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Receipt: %s\n", publish.DefaultReceiptPath(args[0]))
			}
			return err
		},
	}
}

func newAdapter(target config.Target, authenticated bool) (provider.Adapter, error) {
	switch strings.ToLower(target.Provider) {
	case "jira":
		return jira.New(authenticated)
	case "github":
		return githubprovider.New(authenticated, target)
	default:
		return nil, fmt.Errorf("unsupported provider %q", target.Provider)
	}
}
