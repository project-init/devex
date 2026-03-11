# SRE

Command meant as a toolbox command to wrap tools for use in Site Reliability work. Tools are:

- [keygen](../../internal/sre/keygen/README.md)
- [postgres](../../internal/sre/postgres/README.md)
- [release](../../internal/sre/release/README.md)

and usage varies by tool so look at their README's, docs, and usage information to determine how to use them.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
awscli = "latest" # We suggest pinning a version here
"go:github.com/project-init/devex/cmd/sre" = "latest" # We suggest pinning a version here
```

We also suggest making a directory `.sre` which will contain your configuration files. You can start with no files in
the config dir if you want, but by default that is where the tool looks unless overridden using the `--configDir` flag.
