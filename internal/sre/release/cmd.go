package release

import (
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/gommon/pkg/githubclient"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Fetches origin/main, lists commits since the last tag, prompts for a version bump type\n\t(major/minor/patch), and creates + pushes the new tag on origin/main.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			fmt.Printf("Current version: %s\n", current)

			// Safely extract root config from the cobra context
			allowMajor := false
			if cfg, ok := config.GetConfig(cmd.Context()); ok && cfg != nil && cfg.Release.AllowMajorVersionBump != nil {
				allowMajor = *cfg.Release.AllowMajorVersionBump
			} else {
				fmt.Println("\n⚠️  WARNING: allowMajorVersionBump not set in config, defaulting to false")
			}

			if err = displayReleaseCommits(cmd.Context(), token, tip, baseline); err != nil {
				return err
			}

			bumpType, err := selectBumpType(allowMajor)
			if err != nil {
				return err
			}

			next := bump(current, bumpType)

			if bumpType == BumpMajor {
				fmt.Printf("\n⚠️  WARNING: Major version bump indicates BREAKING CHANGES!\n")
			}

			fmt.Printf("\nNew version will be: %s\n", next)

			if err = confirmRelease(next); err != nil {
				return cancelledRelease(err)
			}
			fmt.Printf("Creating and pushing tag %s...\n", next)

			if err = createAndPushTag(next.String(), tip); err != nil {
				return err
			}

			if actionsURL, err := getActionsURL(); err == nil {
				fmt.Printf("\nView release workflow: %s\n", actionsURL)
			}

			return nil
		},
	}
	return cmd
}
