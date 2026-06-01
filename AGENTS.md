# AI Agent Context (AGENTS.md)

This file provides architectural context and conventions for AI agents working on the `devex` project.

## Project Overview

`devex` is a Go CLI for developer experience tooling. It is distributed as a unified `devex` binary with subcommands for SRE operations, localization management, workplan generation, contribution analysis, and component scaffolding.

## Architecture

### CLI Layout

- `cmd/devex/main.go` is the executable entry point. Keep it minimal.
- `internal/root` owns the top-level Cobra command and registers the primary subcommands: `sre`, `localize`, `workplan`, `contributions`, and `components`.
- Each tool exposes a `Command() *cobra.Command` function from its `internal/<tool>` package.
- Core behavior belongs in `internal/`; `cmd/` should only handle executable wiring.
- Prefer adding new functionality as `devex <subcommand>` rather than introducing standalone `cmd/<tool>` binaries, unless there is an explicit distribution reason.

### SRE Toolbox

- `internal/sre/cli.go` builds the `devex sre <tool>` command tree.
- SRE configuration is loaded in `PersistentPreRunE` from `.sre/*.yaml`, or from the directory passed with `--configDir`.
- SRE subcommands live under `internal/sre/<tool>/`.
- Additional SRE-specific guidance is in `internal/sre/AGENTS.md`.

### Code Generation and Templates

- The project uses `embed.FS` and `text/template` for generated files.
- Workplan templates live in `internal/workplan`.
- DB component templates live in `internal/components/db/templates`.
- Generated DB output lives under `gen/db`; prefer changing the generator or templates over editing generated output directly.
- When adding DB templates that should not overwrite user-managed files, keep `nonOverwriteFiles` in `internal/components/db/templates.go` aligned.

## Development Workflow

- Tooling is managed by `mise`; use `mise test`, `mise lint`, and `mise format` from the repo root. `mise run <task>` is also acceptable.
- Treat `mise.toml` and `go.mod` as the Go version sources of truth. Update them together intentionally when changing the Go toolchain or module language version.
- Formatting uses `go fmt ./...` for Go and Prettier for Markdown/YAML/TOML. Prettier config is synced by `scripts/sync-prettier-config.sh`.
- Linting uses `golangci-lint` configured by `golangci.yaml`.
- Tests run with `go test ./... -v`.

## Shared Dependencies

- `github.com/project-init/gommon`: internal shared library.
- `github.com/spf13/cobra`: CLI command framework.
- `gopkg.in/yaml.v3`: YAML configuration.

## Agent Working Notes

- If changing user-facing CLI behavior, update `cmd/devex/README.md` and relevant examples under `cmd/devex/**`.
- Keep command errors returned to Cobra; top-level execution prints errors in `internal/root`.
- Do not add tests for simple Cobra command wiring, argument-count checks, or usage output behavior unless explicitly requested. Prefer direct CLI smoke checks for those cases.
- Prefer source-of-truth files over generated artifacts when making behavioral changes.
