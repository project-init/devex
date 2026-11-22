# devex

Developer Experience Tools - A unified CLI for common developer operations.

## Installation

Add the following to your `mise.toml` file:

```toml
[tools]
"github:project-init/devex" = "latest"
```

Then run:

```shell
mise install
```

## Usage

The `devex` CLI provides a unified interface for all developer experience tools:

```bash
devex <subcommand> [options]
```

## Available Subcommands

- **[sre](cmd/devex/README.md#sre)** - Site Reliability Engineering toolbox for operational tasks

  ```bash
  devex sre <tool> [args]
  ```

  Includes:
  - keygen - Generate API keys
  - postgres - PostgreSQL operations
  - release - Git tag and release management
  - dependencies - Manage and upgrade dependencies

- **[localize](cmd/devex/README.md#localize)** - Audit translations and generate mobile localization bundles

  ```bash
  devex localize audit
  devex localize mobile
  ```

- **[discovery](cmd/devex/README.md#discovery)** - Turn an idea into reviewable discovery artifacts and publish approved
  work to Jira or GitHub

  ```bash
  devex discovery
  devex discovery doctor --harness codex
  devex discovery install-skill
  devex discovery config init
  devex discovery init docs/discoveries audit-logs
  devex discovery validate docs/discoveries/2026-11-21-audit-logs
  devex discovery publish plan docs/discoveries/2026-11-21-audit-logs
  ```

- **[contributions](cmd/devex/README.md#contributions)** - Analyze GitHub PR and review activity for contribution
  signals

  ```bash
  devex contributions collect <config_file>
  devex contributions signal <config_file>
  ```

For detailed documentation on each subcommand, see **[cmd/devex/README.md](cmd/devex/README.md)**.

Run `devex --help` or `devex <subcommand> --help` for more information.

## Documentation

- **[Full CLI Documentation](cmd/devex/README.md)** - Complete guide for all subcommands
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Contribution Guide](./CONTRIBUTING.md)
