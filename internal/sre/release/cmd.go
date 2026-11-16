package release

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/project-init/gommon/pkg/githubclient"
	"github.com/spf13/cobra"
)

const bumpFlag = "bump"

// flags are the release command's options.
type flags struct {
	bump   string
	dryRun bool
	noWait bool
}

// releaseJSON is the --output json document.
type releaseJSON struct {
	CurrentVersion string           `json:"currentVersion"`
	NextVersion    string           `json:"nextVersion"`
	Bump           string           `json:"bump"`
	SuggestedBump  string           `json:"suggestedBump"`
	DryRun         bool             `json:"dryRun"`
	Tagged         bool             `json:"tagged"`
	Changes        []releaseLine    `json:"changes"`
	Workflows      []workflowResult `json:"workflows"`
}

func Command() *cobra.Command {
	var f flags
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Tag origin/main with the next version after previewing its changes",
		Long: "Fetch origin/main, list its commits since the last tag grouped into features, fixes, and other changes, " +
			"suggest a version bump from their conventional-commit titles, then create and push the new tag and wait " +
			"for the release workflow. --bump and --yes run it without a terminal; --dry-run stops before tagging.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), ui.New(cmd), f)
		},
	}
	cmd.Flags().StringVar(&f.bump, bumpFlag, "", "version bump: patch, minor, or major (prompts when omitted)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "print the next version and changes without creating a tag")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "return after pushing the tag instead of waiting for the release workflow")
	return cmd
}

func run(ctx context.Context, u *ui.UI, f flags) error {
	allowMajor, majorConfigured := false, false
	if cfg, ok := config.GetConfig(ctx); ok && cfg != nil && cfg.Release.AllowMajorVersionBump != nil {
		allowMajor, majorConfigured = *cfg.Release.AllowMajorVersionBump, true
	}
	selected, err := validateFlags(u, f, allowMajor)
	if err != nil {
		return err
	}
	token, err := githubclient.ResolveToken()
	if err != nil {
		return githubAuthError(err)
	}
	if err := requireGitHubOrigin(ctx); err != nil {
		return err
	}
	rel, err := loadRelease(ctx, u, token)
	if err != nil {
		return err
	}

	suggested, breakingBlocked := suggestBump(rel.lines, allowMajor)
	if breakingBlocked {
		u.Warn("a change marks itself breaking, but release.allowMajorVersionBump is off; suggesting minor")
	}
	if !u.JSON() {
		u.Status("Current version: %s", rel.current)
		printReleaseLines(u, rel.baseline, rel.lines)
	}
	if f.bump == "" {
		if f.dryRun {
			selected = suggested
		} else {
			if !majorConfigured {
				u.Status("Major bumps are off: release.allowMajorVersionBump is unset.")
			}
			if selected, err = selectBumpType(ctx, u, allowMajor, suggested); err != nil {
				return err
			}
		}
	}
	next := bump(rel.current, selected)
	out := releaseJSON{
		CurrentVersion: rel.current.String(),
		NextVersion:    next.String(),
		Bump:           selected.String(),
		SuggestedBump:  suggested.String(),
		DryRun:         f.dryRun,
		Changes:        rel.lines,
	}
	if f.dryRun {
		return finish(u, out, fmt.Sprintf("Dry run: the next version would be %s (%s). No tag created.", next, selected))
	}

	if selected == BumpMajor {
		u.Warn("a major version bump signals breaking changes")
	}
	u.Status("Next version: %s", next)
	if err = confirmRelease(ctx, u, next); err != nil {
		return err
	}
	pushedAt := time.Now()
	err = u.Spin(ctx, fmt.Sprintf("Pushing tag %s", next), func(ctx context.Context) error {
		return createAndPushTag(ctx, next.String(), rel.tip)
	})
	if err != nil {
		return err
	}
	out.Tagged = true
	u.Success("Pushed tag %s", next)
	if actionsURL, err := getActionsURL(ctx); err == nil {
		u.Status("Release workflow: %s", actionsURL)
	}
	if f.noWait {
		return finish(u, out, "")
	}
	out.Workflows, err = waitForRelease(ctx, u, token, next.String(), rel.tip, pushedAt)
	return errors.Join(finish(u, out, ""), err)
}

// session is how the command can get answers; *ui.UI provides it.
type session interface {
	Interactive() bool
	Yes() bool
	JSON() bool
}

// validateFlags checks --bump, and that a run without a terminal has every answer, before any
// network work.
func validateFlags(u session, f flags, allowMajor bool) (bumpType, error) {
	var selected bumpType
	if f.bump != "" {
		parsed, err := parseBumpType(f.bump, allowMajor)
		if err != nil {
			return 0, err
		}
		selected = parsed
	} else if !f.dryRun && (!u.Interactive() || u.JSON()) {
		// JSON mode prints no preview to choose from, so it needs the answer up front.
		return 0, &ui.MissingFlagError{Flag: bumpFlag}
	}
	if !f.dryRun && (!u.Interactive() || u.JSON()) && !u.Yes() {
		return 0, &ui.MissingFlagError{Flag: ui.YesFlag}
	}
	return selected, nil
}

// pendingRelease is origin/main's tip, its current version, and the commits since that version.
type pendingRelease struct {
	tip      string
	baseline string
	current  version
	lines    []releaseLine
}

func loadRelease(ctx context.Context, u *ui.UI, token string) (pendingRelease, error) {
	var rel pendingRelease
	err := u.Spin(ctx, "Fetching origin/main and its pull requests", func(ctx context.Context) error {
		var err error
		if rel.tip, err = fetchOriginMain(ctx); err != nil {
			return err
		}
		if rel.current, rel.baseline, err = versionAtTip(ctx, rel.tip); err != nil {
			return err
		}
		rel.lines, err = releaseCommitLines(ctx, token, rel.tip, rel.baseline)
		return err
	})
	return rel, err
}

// failedConclusions are the workflow conclusions that mean the release did not complete; success,
// neutral, and skipped do not.
var failedConclusions = map[string]bool{
	"failure": true, "cancelled": true, "timed_out": true, "action_required": true, "startup_failure": true, "stale": true,
}

// waitForRelease waits on the workflow runs the tag started and reports each one.
func waitForRelease(ctx context.Context, u *ui.UI, token, tag, sha string, pushedAt time.Time) ([]workflowResult, error) {
	owner, repo, err := getOriginGitHubRepo(ctx)
	if err != nil {
		return nil, err
	}
	client, err := githubclient.New(token, "")
	if err != nil {
		return nil, err
	}
	var results []workflowResult
	err = u.Spin(ctx, "Waiting for the release workflow", func(ctx context.Context) error {
		results, err = waitForWorkflow(ctx, client, owner, repo, tag, sha, pushedAt, defaultWaitTiming)
		return err
	})
	switch {
	case errors.Is(err, errNoWorkflowRun):
		u.Warn("no release workflow started for %s", tag)
		return nil, nil
	case err != nil && ctx.Err() == nil:
		// The tag is out; failing to watch its workflow is not a failed release.
		u.Warn("stopped watching the release workflow: %v", err)
		return nil, nil
	case err != nil:
		return nil, err
	}
	var failed []string
	for _, result := range results {
		if !failedConclusions[result.Conclusion] {
			u.Success("%s finished with %s: %s", result.Name, result.Conclusion, result.URL)
			continue
		}
		u.Warn("%s finished with %s: %s", result.Name, result.Conclusion, result.URL)
		failed = append(failed, result.Name)
	}
	if len(failed) > 0 {
		return results, fmt.Errorf("release workflow failed: %s", strings.Join(failed, ", "))
	}
	return results, nil
}

// finish writes the JSON document in --output json mode, and otherwise prints message.
func finish(u *ui.UI, out releaseJSON, message string) error {
	if u.JSON() {
		if out.Workflows == nil {
			out.Workflows = []workflowResult{} // [] rather than null for scripts
		}
		return u.WriteJSON(out)
	}
	if message != "" {
		u.Status("%s", message)
	}
	return nil
}
