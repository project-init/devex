# Dependencies

Upgrade mise tools, Go modules, and Buf dependencies, and keep every Go version pin in the
repository on one version.

A Go project states its Go version in several places: `mise.toml`, the `go.mod` directives,
Dockerfile golang images, CI `setup-go` steps. Tools that bump one leave the rest behind.
`mise upgrade --bump` moves `mise.toml`; Dependabot raises the `go.mod` floor; neither
touches the Docker tag. The golang images set `GOTOOLCHAIN=local`, so a `go.mod` floor above
the image tag fails the build. `upgrade --go` moves every pin together, and `check` catches
drift from every other source.

## Usage

```shell
# Move every Go pin to the latest patch, then upgrade modules under that toolchain.
devex sre dependencies upgrade --go

# Upgrade everything listed in dependencies.upgrade.
devex sre dependencies upgrade --all

# Plan and verify without writing anything.
devex sre dependencies upgrade --all --show-commands

# Take a deliberate minor bump, or settle existing drift.
devex sre dependencies upgrade --go --go-version 1.27.1

# Fail when pins disagree. Offline; run it in pull request CI.
devex sre dependencies check
```

A `.sre` directory is optional for these commands. Without one, they run on the defaults
below.

### Flags

| Flag              | Effect                                                                                            |
| ----------------- | ------------------------------------------------------------------------------------------------- |
| `--go`            | Sync every Go pin, then run `go get -u ./...` and `go mod tidy` in each module                    |
| `--mise`          | Run `mise upgrade --bump --exclude go`; only `--go` moves Go                                      |
| `--buf`           | Run `buf dep update`                                                                              |
| `--all`           | Enable the ecosystems in `dependencies.upgrade`                                                   |
| `--go-version`    | Move Go to this published release, ignoring `target` and waiving existing drift                   |
| `--show-commands` | Resolve the target and verify image tags, then print each change and command without running them |

## Configuration

Every field is optional.

```yaml
dependencies:
  upgrade: [mise, go, buf] # what --all runs
  go:
    target: patch # patch | latest
    directive: none # none | minor | exact
    images: [golang] # images that carry the Go toolchain
    exclude: [] # globs skipped on top of .gitignore
    pins: [] # locations the finders cannot recognize
```

### `target`

- **`patch`** (default): the latest patch of the current minor, so 1.26.6 becomes 1.26.8.
  Patches carry bug and security fixes and are safe to automate.
- **`latest`**: the latest release, crossing minors.

devex resolves releases from go.dev, so mise is optional. It never moves Go backwards.

### `directive`

`go.mod`'s `go` directive is a floor: the oldest Go that may build the module. A library's
floor binds every consumer, so devex leaves it alone unless told otherwise.

- **`none`** (default): never change it; require only that it stays at or below the
  toolchain.
- **`minor`**: hold it on the toolchain's minor line, at `X.Y.0` when the minor changes.
- **`exact`**: set it to the toolchain version. Suits applications that build in the golang
  image.

devex moves an existing `toolchain` directive with the toolchain and never adds one. It never
lowers a floor: a `go` directive above the target fails the run. Under every policy, a
`go.work` `go` directive rises to the highest floor among the modules it uses, the lowest
version the go command accepts; `go work use` raises it again whenever `go mod tidy` lifts a
module's floor.

### `images`

A Dockerfile pin counts when its image's last path segment matches an entry, so `golang`
matches `golang`, `docker.io/library/golang`, and `public.ecr.aws/docker/library/golang`. An
entry containing `/` must match the whole repository, such as `cgr.dev/chainguard/go`.

### `exclude`

Discovery reads tracked files and untracked files `.gitignore` allows, and skips
`testdata/` and `vendor/` at any depth. Patterns follow `.gitignore`: one without `/` matches a
name at any depth, one with `/` matches from the repository root, and `**` as a whole
segment spans directories. A pattern that matches a directory excludes everything under it, and a trailing
`/` matches directories only, so `examples` and `examples/` both skip the whole tree.
Character classes (`[ab]`, `[!ab]`, `[^ab]`) and backslash escapes work, and like other
wildcards a class never matches `/`. POSIX classes such as `[[:digit:]]` and a pattern
starting with `!` are errors.

### `pins`

Declare any location the built-in finders miss. `files` takes the same globs as `exclude`;
`pattern` is a regular expression whose `version` group captures the version. devex replaces
only that group.

```yaml
dependencies:
  go:
    pins:
      - files: ["docker-compose*.yaml"]
        pattern: 'GO_IMAGE_TAG:\s*"?(?P<version>\d+\.\d+(?:\.\d+)?)'
      - files: ["build/*.Dockerfile"]
        pattern: 'ENV GO_VERSION=(?P<version>\S+)'
```

## What it finds

| Pin                 | Where                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| mise                | `go` under `[tools]` as a string, an inline table, or `go.version`; `version` in a `[tools.go]` table; or a root `tools.go`, in every mise config: `mise.toml`, `.mise.toml`, their `<env>` and `local` variants, `config*.toml` under `mise/` or `.mise/`, and `conf.d/*.toml`. Other forms, including aliases such as `golang`, `core:go`, and `aqua:golang/go`, produce a warning, though aliases defined under `[tool_alias]` go unnoticed; lines continuing a multi-line string, array, or inline table are ignored |
| `go.mod`, `go.work` | the `go` directive (a floor) and the `toolchain` directive; a file that fails to parse stops the run                                                                                                                                                                                                                                                                                                                                                                                                                     |
| Dockerfile          | in `Dockerfile`, `Dockerfile.*`, `*.Dockerfile`, and their `Containerfile` equivalents, in any case and never with a source extension such as `.go`: golang images in `FROM`, and the ARGs declared before the first `FROM` that feed them                                                                                                                                                                                                                                                                               |
| setup-go            | literal `go-version` inputs in `.github/workflows` and `.github/actions/*/action.yml`; block scalars produce a warning                                                                                                                                                                                                                                                                                                                                                                                                   |
| `.go-version`       | the first version line                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `.tool-versions`    | the `golang` or `go` entry                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| declared            | each `pins` entry                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |

A pin keeps its precision: `golang:1.26` stays `golang:1.26` through a patch upgrade, and the
tag suffix, such as `-alpine3.24`, stays put. devex warns about versions it sees but cannot
manage, such as `golang:latest`, a `${{ matrix.go }}` expression, or an ARG with no default.

When an ARG supplies a Dockerfile's version, devex also searches the rest of the repository
for that ARG's name. A `--build-arg` set in a compose file, workflow, or Makefile overrides
the Dockerfile default, and neither `upgrade` nor `check` can see it unless it is declared as
a pin, so each mention produces a warning.

## How `upgrade --go` runs

1. **Plan, writing nothing.** Discover every pin, derive the current version, resolve the
   target from go.dev, and query the registry for each rewritten or digest-pinned image
   tag. The planned versions must pass `check`. Pins that disagree, an unpublished tag, or a
   floor above the target stop the run here, with every file untouched.
2. **Write.** When a pin mise reads moves, in a mise config or `.tool-versions`,
   `mise install go@<target>` runs first if mise is on `PATH`, so its shims can run the new
   Go; it writes no config. devex then confirms every planned edit against the
   file's current text before changing any file. It replaces only the version digits, so
   comments, formatting, mise tool options, and any `go` prefix survive; in `go.mod` and
   `go.work` that yields the line `go mod edit` would write. A mise lock file, such as
   `mise.lock` or `mise.ci.lock`, keeps the old Go, so devex warns; run `mise lock` to
   refresh it.
3. **`--mise`.** Run `mise upgrade --bump --exclude go`, so Go moves only with every other
   pin. It runs after the write because it can shift the text of files devex edits in place,
   such as `.tool-versions`.
4. **Upgrade modules.** Run `go get -u ./...` and `go mod tidy` in every module with
   `GOTOOLCHAIN=go<target>` and `GOWORK=off`, so each works on its own `go.mod`. A
   dependency whose latest release needs a newer Go is held at its current version,
   reported, and the rest upgrade. A vendored module gets `go mod vendor`, unless a `go.work`
   sits beside it. Holding a module back while its dependencies move can break the build, so
   a module with holds must compile on the host, tests included, or the run fails and names
   the Go version the holds need; a module with no package for the host passes. Once every
   module has upgraded, `go work use` lifts every `go.work` floor that `go mod tidy` left
   behind, and a vendored workspace gets `go work vendor`.

   Holding reads go's error text, so it has limits. A module that is not yet in `go.mod`,
   such as one a dependency's new release pulls in, cannot be held, and the run fails; raise
   the target instead. A hold can also quietly keep back modules that need a newer release of
   the held one, and devex does not report those.

5. **Verify.** Run `check`; any drift fails the run.
6. **`--buf`.** Run `buf dep update`.

Every command runs with an empty `GOROOT`, so a root the caller exports, such as `go run`'s,
cannot pair a child's `go` with another release's compiler.

A failure after step 2 leaves the written files in place. In CI the job fails and no pull
request opens; locally, git is the rollback.

### Image tags

devex queries each rewritten tag's registry anonymously before writing. An unpublished tag,
such as a new Go release that dropped the pinned Alpine suffix, fails the plan and names the
tag; choose a new suffix by hand. devex resolves a registry named by an ARG through that
ARG's default. It cannot query one supplied only as a `--build-arg`, so it writes that tag
unverified. A registry that needs credentials produces a warning and an unverified tag.

A digest pin (`@sha256:…`) moves to the new tag's digest, even when the tag text stays the
same, as `golang:1.26` does across patches. A registry that needs credentials fails the plan
for a digest-pinned image that changes tag. When the tag stays the same, any failed refresh
keeps the old digest and devex warns. An image without a tag, or with a whole reference or a
digest from a build arg, such as `FROM ${GO_IMAGE}` or `@${GO_DIGEST}`, stays unmanaged with a
warning.

## `check`

`check` makes no network calls. It fails when toolchain pins disagree, when any `go`
directive sits above the toolchain, when a `go.work` `go` directive sits below that of a
module it uses (the go command refuses to build that workspace), or when the `directive` policy
does not hold. Run it in pull request CI:

```yaml
- run: devex sre dependencies check
```

## Tests

Unit tests fake go.dev, registries, and every external command. A live check against go.dev
and Docker Hub runs behind the `integration` build tag:

```shell
go test -tags integration ./internal/sre/dependencies/...
```
