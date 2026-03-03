# Release

Simple tool used to create a release (as a tag) for a github repository. Meant to be added to repositories (like this
one) that use tagging as a way to kick off/generate releases.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/release" = "latest"
```

Then you can run the cmd `release` to generate a git tag and trigger your release if it is coupled with a GH workflow
like [this](../../.github/workflows/release.yaml).