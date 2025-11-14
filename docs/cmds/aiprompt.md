# AIPrompt

The aiprompt cmd is meant to use a set of templates, with defined arguments to use in order to prompt `Gemini` to start
an AI process "on rails". The goal being that whoever has a prompt that consistently produces results, can add that in
a template form such that every dev working in the repo can use going forward.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/aiprompt" = "latest"
```

Then you can run the cmd like

```shell
aiprompt .prompts
```

which will load up the prompts in an interactive way to populate the data to use in the template. By default `gemini` is
used as the agent, but that is overrideable with

```yaml
agent:
  name: <agent_exe>
  arguments:
    - <argument_1>
    - <argument_2>
    - ...
```

## Why Gemini as the default?

The Gemini CLI works very well, and we assume most project we work with will have Google integration in some capacity,
which makes it a safe candidate for common use across projects/companies.