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

- **[workplan](cmd/devex/README.md#workplan)** - Generate and publish workplan investigations to JIRA

  ```bash
  devex workplan generate <directory> <title>
  devex workplan publish <workplan_path>
  ```

- **[contributions](cmd/devex/README.md#contributions)** - Analyze GitHub PR and review activity for contribution signals

  ```bash
  devex contributions collect <config_file>
  devex contributions signal <config_file>
  ```

- **[components](cmd/devex/README.md#components)** - Generate component skeleton code from configuration
  ```bash
  devex components <configuration_file_path>
  ```

For detailed documentation on each subcommand, see **[cmd/devex/README.md](cmd/devex/README.md)**.

Run `devex --help` or `devex <subcommand> --help` for more information.

## Documentation

- **[Full CLI Documentation](cmd/devex/README.md)** - Complete guide for all subcommands
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Contribution Guide](./CONTRIBUTING.md)
