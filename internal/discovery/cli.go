package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

const defaultConfigPath = ".sre/discovery.yaml"

func Command() *cobra.Command {
	var configurationPath string
	var harnessValues []string
	command := &cobra.Command{
		Use:           "discovery",
		Short:         "Turn product discovery into reviewable plans and published work",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(cmd, ".", configurationPath, harnessValues)
		},
	}
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, harnessFlag, nil, "AI harness: codex, claude, cursor, or all")
	command.AddCommand(initCommand())
	command.AddCommand(validateCommand())
	command.AddCommand(configCommand())
	command.AddCommand(installSkillCommand())
	command.AddCommand(setupCommand())
	command.AddCommand(doctorCommand())
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
			for _, result := range installed {
				_, _ = fmt.Fprintf(
					cmd.OutOrStdout(),
					"%s run-discovery skill for %s: %s\n",
					strings.ToUpper(string(result.State)),
					result.Harness,
					result.Path,
				)
			}
			return nil
		},
	}
	command.Flags().StringSliceVar(&harnesses, "harness", []string{"all"}, "AI harness: codex, claude, cursor, or all")
	command.Flags().BoolVar(&force, "force", false, "replace different existing skill files")
	return command
}

func initCommand() *cobra.Command {
	var configPath string
	command := &cobra.Command{
		Use:   "init <directory> <name>",
		Short: "Create a discovery document and provider-neutral work breakdown",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultLabels, err := loadDefaultLabels(configPath)
			if err != nil {
				return err
			}
			directory, err := templates.GenerateWithOptions(args[0], args[1], templates.GenerateOptions{
				DefaultLabels: defaultLabels,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created discovery bundle at %s\n", directory)
			return nil
		},
	}
	command.Flags().StringVar(&configPath, "config", defaultConfigPath, "discovery configuration file")
	return command
}

func loadDefaultLabels(path string) ([]string, error) {
	configuration, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]string(nil), configuration.DefaultLabels...), nil
}

func validateCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "validate <bundle-directory>",
		Short:       "Validate a discovery document and work breakdown",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
			bundle, err := artifact.Load(args[0])
			if err != nil {
				return err
			}
			if u.JSON() {
				return u.WriteJSON(map[string]any{"directory": bundle.Directory, "workItems": len(bundle.WorkBreakdown.Items), "digest": bundle.Digest()})
			}
			u.Printf("Valid discovery bundle %s (%d work items, %s)\n", bundle.Directory, len(bundle.WorkBreakdown.Items), bundle.Digest())
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
	var options publish.PlanOptions
	command := &cobra.Command{
		Use:         "plan <bundle-directory>",
		Short:       "Create a read-only, reviewable publication plan",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
			bundle, err := artifact.Load(args[0])
			if err != nil {
				return err
			}
			configuration, err := config.Load(configPath)
			if err != nil {
				return err
			}
			resolvedTargetName, target, err := configuration.ResolveTarget(targetName)
			if err != nil {
				return fmt.Errorf("resolve publication target from %s: %w", configPath, err)
			}
			targetName = resolvedTargetName
			adapter, err := newAdapter(target, options.Sync)
			if err != nil {
				return err
			}
			var plan *provider.Plan
			create := func(ctx context.Context) error {
				plan, err = publish.CreatePlan(ctx, bundle, targetName, target, adapter, options)
				return err
			}
			if options.Sync {
				err = u.Spin(cmd.Context(), "Reading published work", create)
			} else {
				err = create(cmd.Context())
			}
			if err != nil {
				return err
			}
			if outputPath == "" && options.Sync {
				outputPath = publish.DefaultSyncPlanPath(bundle.Directory, targetName, plan.GeneratedAt)
			} else if outputPath == "" {
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
			for _, warning := range plan.Warnings {
				u.Warn("%s", warning)
			}
			if u.JSON() {
				return u.WriteJSON(map[string]any{"path": outputPath, "counts": actionCounts(plan.Operations), "plan": plan})
			}
			printPlan(u, outputPath, plan)
			return nil
		},
	}
	command.Flags().StringVar(&configPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringVar(&targetName, "target", "", "named publication target (overrides default_target)")
	command.Flags().StringVar(&outputPath, "out", "", "publication plan output path")
	command.Flags().BoolVar(&options.Sync, "sync", false,
		"read published work and plan updates and link removals (needs provider credentials)")
	command.Flags().BoolVar(&options.Force, "force", false, "with --sync, overwrite issues edited since devex last wrote them")
	return command
}

func applyCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "apply <plan-file>",
		Short:       "Apply a frozen publication plan and write a resumable receipt",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
			plan, err := publish.LoadPlan(args[0])
			if err != nil {
				return err
			}
			adapter, err := newAdapter(plan.Target, true)
			if err != nil {
				return err
			}
			var outcomes []publish.Progress
			var archived string
			receipt, err := publish.Apply(cmd.Context(), args[0], adapter, publish.ApplyHooks{
				Remote: u.Spin,
				Progress: func(progress publish.Progress) {
					outcomes = append(outcomes, progress)
					printProgress(u, progress)
				},
				Archived: func(path string) {
					archived = path
					u.Status("Archived the receipt of the plan this one replaced: %s", path)
				},
			})
			if receipt == nil {
				return err
			}
			receiptPath := publish.DefaultReceiptPath(args[0])
			summary := summarize(plan, receipt, outcomes)
			if failedThisRun(outcomes) && cmd.Context().Err() == nil {
				// The progress line already named the operation and its error.
				err = errors.New("apply stopped at the failed operation above")
			}
			if u.JSON() {
				return errors.Join(u.WriteJSON(applyJSON(receiptPath, archived, receipt.Status, summary, outcomes)), err)
			}
			u.Printf("%s\n", summary)
			u.Printf("Receipt: %s\n", receiptPath)
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
