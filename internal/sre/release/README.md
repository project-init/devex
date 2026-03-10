# Release

Simple tool used to create a release (as a tag) for a github repository. Meant to be added to repositories (like this
one) that use tagging as a way to kick off/generate releases.

## Configuration

```yaml
release:
```

## Usage

```shell
sre release --config .sre
```

The above will generate a git tag and trigger your release if it is coupled with a GH workflow like [this](../../../.github/workflows/release.yaml).

#### Description

The release cmd does a simple git tag and push and assumes the GH workflow covers the rest. Future upgrades will likely
include a better UI with more content, and configuration that limits what can be done such as prohibiting major version
bumps.
