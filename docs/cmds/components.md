# Components

The `components` cmd is meant to use a yaml configuration file, which will define what components should be generated,
and any metadata/settings to use when performing the generation.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/components" = "latest"
```

Then you can run the cmd like

```shell
components .components
```

Example config can be found [here](../../cmd/components/example_config.yaml), but a general example looks like

```yaml
outputDirectory: "gen"

db:
  schemaName: data_platform
```

## Components

### DB

The DB components will output a general setup with users, schema, and iam permissions setup. It also has some default
migrations and post release scripts to run for DB setup. It also has a sqlc configuration, as the assumption is you are
working with a Postgres cluster for a Go service.