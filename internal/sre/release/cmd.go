package release

import (
	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/project-init/gommon/pkg/githubclient"
	"github.com/spf13/cobra"
)

const bumpFlag = "bump"

func Command() *cobra.Command {
	var bumpValue string
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Fetches origin/main, lists commits since the last tag, prompts for a version bump type\n\t(major/minor/patch), and creates + pushes the new tag on origin/main.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
			ctx := cmd.Context()

			allowMajor, majorConfigured := false, false
			if cfg, ok := config.GetConfig(ctx); ok && cfg != nil && cfg.Release.AllowMajorVersionBump != nil {
				allowMajor, majorConfigured = *cfg.Release.AllowMajorVersionBump, true
			}
			// Fail on missing or invalid answers before any network work.
			var selected bumpType
			if bumpValue != "" {
				parsed, err := parseBumpType(bumpValue, allowMajor)
				if err != nil {
					return err
				}
				selected = parsed
			} else if !u.Interactive() {
				return &ui.MissingFlagError{Flag: bumpFlag}
			}
			if !u.Interactive() && !u.Yes() {
				return &ui.MissingFlagError{Flag: ui.YesFlag}
			}

			token, err := githubclient.ResolveToken()
			if err != nil {
				return githubAuthError(err)
			}
			if err := requireGitHubOrigin(); err != nil {
				return err
			}

			tip, err := fetchOriginMain()
			if err != nil {
				return err
			}

			current, baseline, err := versionAtTip(tip)
			if err != nil {
				return err
			}

			u.Status("Current version: %s", current)

			if err = displayReleaseCommits(ctx, u, token, tip, baseline); err != nil {
				return err
			}

			if bumpValue == "" {
				if !majorConfigured {
					u.Status("Major bumps are off: release.allowMajorVersionBump is unset.")
				}
				if selected, err = selectBumpType(ctx, u, allowMajor); err != nil {
					return err
				}
			}

			next := bump(current, selected)

			if selected == BumpMajor {
				u.Warn("a major version bump signals breaking changes")
			}

			u.Status("Next version: %s", next)

			if err = confirmRelease(ctx, u, next); err != nil {
				return err
			}
			u.Status("Creating and pushing tag %s...", next)

			if err = createAndPushTag(next.String(), tip); err != nil {
				return err
			}
			u.Success("Pushed tag %s", next)

			if actionsURL, err := getActionsURL(); err == nil {
				u.Status("View release workflow: %s", actionsURL)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&bumpValue, bumpFlag, "", "version bump: patch, minor, or major (prompts when omitted)")
	return cmd
}
