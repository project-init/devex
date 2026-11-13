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
