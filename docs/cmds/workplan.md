# Workplan

The workplan cmd is meant to generate a workplan template for use in investigating a feature/problem and rendering an
idea of how much work is needed to complete it. Currently, the workplan is set to publish to JIRA and the publishing
feature will not work with any other work product tool at this time.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/workplan" = "latest"
```

Then you can run the cmd like

```shell
workplan generate docs/investigations/infrastructure/moveToProjectInitStack/workplan.yaml
```

to generate a problem and workplan template. Then once you fill in and review that content you can run

```shell
export JIRA_URL=<your_url>
export JIRA_EMAIL=<your_jira_email>
export JIRA_API_KEY=<your_api_key>

# Run this command next
workplan publish docs/investigations/infrastructure/moveToProjectInitStack/workplan.yaml
```

You will likely want to set the JIRA env variables in a zshrc setup of some kind so you don't have to repeat the first 3
steps.

## Why Jira?

Most common tool that the project-init team has used, so that is what we set up to work with. We aren't declaring that
JIRA is the best tool, just that it is what we have the knowledge on how to work with so we set up publishing towards
it.
