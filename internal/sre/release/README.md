# Release

Simple tool used to create a release (as a tag) for a github repository. Meant to be added to repositories (like this
one) that use tagging as a way to kick off/generate releases.

## Configuration

```yaml
release:
```

## Usage

```shell
devex sre release

# Without prompts, as in CI:
devex sre release --bump patch --yes
```

`--bump` takes `patch`, `minor`, or `major`; `major` needs `allowMajorVersionBump: true`. Without `--bump` the tool prompts on a terminal, preselecting the bump the commit titles suggest, and fails with `pass --bump` otherwise. `--yes` skips the confirmation.

The preview groups commits into features, fixes, and other changes by their conventional-commit type. A `feat` title suggests a minor bump, a `!` after the type or `BREAKING CHANGE` in the title suggests major (minor when majors are off), and anything else suggests patch. Only titles count: a `BREAKING CHANGE:` footer in a pull request body does not. Merge commits list as themselves under other changes.

After pushing the tag, the tool waits for the runs the tag started. When a run appears it prints that workflow's page, once per workflow file, and when the runs finish it prints each result. `--no-wait` returns right after the push and prints the repository Actions tab, since no run is available yet to name the workflow. The command exits non-zero when a run fails or is cancelled; if it cannot watch the runs, it warns and exits 0, since the tag is already pushed. `--output json` needs `--bump` and `--yes` (except with `--dry-run`), because it prints no preview to choose or confirm from.

```shell
# Preview the next version and changes without tagging; --bump is optional here.
devex sre release --dry-run

# Script it: the versions, the suggested bump, every change, and the workflow results.
devex sre release --bump minor --yes --output json
```

The above will generate a git tag and trigger your release if it is coupled with a GH workflow like [this](../../../.github/workflows/release.yaml).

GitHub auth is required (`gh auth login` or `GITHUB_TOKEN`). `origin` must be a github.com remote.

Before confirmation the tool lists commits on `origin/main` since the previous tag. See [PR preview](#pr-preview) for what is included.

#### PR preview

Before you confirm the tag, the tool fetches `origin/main` and prints the commits on that tip.

- **With an existing tag:** `git log <tag>..origin/main --first-parent`. The tag is the latest tag on the first-parent history of `origin/main`.
- **No tags yet:** heading `all history` — the full first-parent history of `origin/main`.

Squash-merge subjects that end in `(#N)` are shown with that pull request's title and author, loaded in one GitHub request. A commit without that suffix is listed as itself (short SHA, git author, and subject). The new tag is created on the fetched `origin/main` tip, including when the checked-out branch is behind.

**Not included:** commits that are not on the first-parent history of `origin/main`, such as commits that only exist on a merged side branch.

#### Description

The release cmd does a simple git tag and push and assumes the GH workflow covers the rest.
Future upgrades will likely include a better UI with more content, and configuration that limits what can be done.
