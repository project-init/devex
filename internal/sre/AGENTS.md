# SRE Toolbox Agent Context

This file applies to work under `internal/sre`.

## Command Structure

- `internal/sre/cli.go` defines the `devex sre <tool>` root command.
- Add new top-level SRE tools by creating `internal/sre/<tool>/cmd.go` with a `Command() *cobra.Command` function, then register it in `sre.Command()`.
- Use nested packages for tool areas when the behavior is more than a single command, following patterns like `postgres/access` and `analyze/<area>`.
- Keep Cobra command constructors focused on flags, argument validation, configuration lookup, and dispatch. Put reusable behavior in helper functions or narrower packages.

## Configuration

- SRE config is loaded once by `PersistentPreRunE` in `internal/sre/cli.go`.
- The config directory defaults to `.sre` and can be overridden with `--configDir`.
- `config.LoadConfig` reads sorted `*.yaml` files from the config directory and unmarshals them into one `config.Configuration`.
- Add new config structs under `internal/sre/config`, expose them from `Configuration`, and use explicit `yaml` tags.
- Commands should read configuration with `config.GetConfig(cmd.Context())`; avoid reloading the config directory inside subcommands.
- Keep example files under `.sre/` aligned with new or changed configuration fields.

## External Commands and Interactivity

- `postgres` commands can depend on AWS credentials, IAM auth, `promptui`, and `psql`.
- `release` commands interact with git tags and remotes. Preserve confirmations for mutating operations.
- Keep external command execution isolated in small functions so pure logic remains testable.
- Prefer returning errors from command logic. Avoid adding new `os.Exit` calls in subcommands.

## Documentation and Tests

- Update `cmd/devex/README.md` and any relevant `internal/sre/<tool>/README.md` when user-facing behavior changes.
- Add focused tests for config parsing and pure helpers.
- Avoid tests that require AWS, `psql`, network access, Gemini, or live git remotes unless they are explicitly gated as integration tests.
