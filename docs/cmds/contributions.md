# Contributions

The contributions cmd is meant to generate a signal file which can be used to determine which devs/repos are having the
most impact or potentially are having issues. Can be used on a repo level basis, but the suggestion is to make a repo
such as `github.com/yourorg/contributions` where you run a nightly cron to collect data and create signal from it.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/contributions" = "latest"
```

Then you can run the cmd like

```shell
contributions contributions_config.yaml
```

to generate output like `key: f60a6053cfba2b02b44987d23e24acdb`. Copy/Paste that key and use as you like from there.
