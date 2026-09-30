# Release

Simple tool used to create a release (as a tag) for a github repository. Meant to be added to repositories (like this
one) that use tagging as a way to kick off/generate releases.

## Configuration

```yaml
release:
```

## Usage

```shell
sre release
```

The above will generate a git tag and trigger your release if it is coupled with a GH workflow like [this](../../../.github/workflows/release.yaml).

GitHub auth is required (`gh auth login` or `GITHUB_TOKEN`). `origin` must be a github.com remote.

Before confirmation the tool lists merged pull requests into `main`. See [PR preview](#pr-preview) for what is included.

#### PR preview

Before you confirm the tag, the tool prints merged GitHub pull requests into **`main`** only.

- **With an existing tag:** PRs merged into `main` after the **latest tag's commit time** (from `git describe` and `git log` on that tag).
- **No tags yet:** heading `all history` — every merged PR into `main` (the tool fetches all pages from the GitHub API, 100 PRs per request).

**Not included:** PRs merged into other branches (for example stacked or feature branches), closed-but-unmerged PRs, and commits that never went through a PR.

#### Description

The release cmd does a simple git tag and push and assumes the GH workflow covers the rest.
Future upgrades will likely include a better UI with more content, and configuration that limits what can be done.
