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

Before confirmation the tool lists merged pull requests into `main` since the latest tag, or all such PRs when tagging
for the first time.

#### Description

The release cmd does a simple git tag and push and assumes the GH workflow covers the rest.
Future upgrades will likely include a better UI with more content, and configuration that limits what can be done.
