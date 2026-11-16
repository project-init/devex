# AI Agent Context (AGENTS.md)

This file provides architectural context and conventions for AI agents working on the `devex` project.

## Project Overview

`devex` is a Go CLI for developer experience tooling. It is distributed as a unified `devex` binary with subcommands for SRE operations, localization management, discovery planning, workplan generation (deprecated in favor of discovery), and contribution analysis.

## Architecture

### CLI Layout

- `cmd/devex/main.go` is the executable entry point. Keep it minimal.
- `internal/root` owns the top-level Cobra command and registers the primary subcommands: `sre`, `localize`, `discovery`, `workplan`, and `contributions`.
- Each tool exposes a `Command() *cobra.Command` function from its `internal/<tool>` package.
- Core behavior belongs in `internal/`; `cmd/` should only handle executable wiring.
- Prefer adding new functionality as `devex <subcommand>` rather than introducing standalone `cmd/<tool>` binaries, unless there is an explicit distribution reason.

### SRE Toolbox

- `internal/sre/cli.go` builds the `devex sre <tool>` command tree.
- SRE configuration is loaded in `PersistentPreRunE` from `.sre/*.yaml`, or from the directory passed with `--config-dir` (`--configDir` is an alias). Loading is strict: an unknown key fails. The loader skips files with a top-level `targets` key, which hold `devex discovery` configuration.
- SRE subcommands live under `internal/sre/<tool>/`.
- Additional SRE-specific guidance is in `internal/sre/AGENTS.md`.

### Code Generation and Templates

- The project uses `embed.FS` and `text/template` for generated files.
- Workplan templates live in `internal/workplan`. `workplan` is deprecated; build new planning features in `internal/discovery`.

## Development Workflow

- Tooling is managed by `mise`; use `mise test`, `mise lint`, and `mise format` from the repo root. `mise run <task>` is also acceptable.
- Treat `mise.toml` and `go.mod` as the Go version sources of truth. Update them together intentionally when changing the Go toolchain or module language version.
- Formatting uses `go fmt ./...` for Go and Prettier for Markdown/YAML/TOML. Prettier config is synced by `scripts/sync-prettier-config.sh`.
- Linting uses `golangci-lint` configured by `.golangci.yaml`.
- Tests run with `go test ./... -v`.

## Shared Dependencies

- `github.com/project-init/gommon`: internal shared library.
- `github.com/spf13/cobra`: CLI command framework.
- `gopkg.in/yaml.v3`: YAML configuration.

## Agent Working Notes

- If changing user-facing CLI behavior, update `cmd/devex/README.md` and relevant examples under `cmd/devex/**`.
- Render output through `internal/ui`: data to stdout with `Printf`, `Table`, or `WriteJSON`; status, warnings, spinners, and progress to stderr. Prompt with `ui.Select` or `ui.Confirm`, and give each prompt a flag. Mark a command that supports `--output json` with `ui.JSONAnnotation`.
- Keep command errors returned to Cobra; top-level execution prints errors in `internal/root`. Never call `log.Fatal`, `os.Exit`, or `panic` outside `cmd/devex/main.go`.
- Write a file devex rewrites whole and a later run must parse, such as a receipt, a localize catalog, or collected data, with `atomicfile.Write`, so an interrupted run never leaves it half-written. Edit other source files in place, as `sre dependencies` does, so they keep their owner and links. Decode YAML configuration with `strictyaml.Decode`, which fails on unknown keys and extra documents.
- Use `cmd.Context()` for network and long-running work; `internal/root` cancels it on Ctrl-C or SIGTERM.
- Do not add tests for simple Cobra command wiring, argument-count checks, or usage output behavior unless explicitly requested. Prefer direct CLI smoke checks for those cases.
- Prefer source-of-truth files over generated artifacts when making behavioral changes.
