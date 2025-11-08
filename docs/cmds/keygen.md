# Keygen

The keygen cmd is meant to generate a random character string that can be used as an api key.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/keygen" = "latest"
```

Then you can run the cmd like

```shell
keygen
```

to generate output like `key: f60a6053cfba2b02b44987d23e24acdb`. Copy/Paste that key and use as you like from there.
