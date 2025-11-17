# AIPrompt

The `aiprompt` cmd is meant to use a set of templates, with defined arguments to use in order to prompt an ai agent to
start an AI process "on rails". The goal being that whoever has a prompt that consistently produces results, can add
that in a template form such that every dev working in the repo can use it going forward.

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

which will load up the prompts in an interactive way to populate the data to use in the template. Example prompts are
in [here](../../cmd/aiprompt/.prompts), but a general example looks like

```yaml
args:
  - query: What event name should be used?
  - query: What event type should be used?
    options:
      - eventType1
      - eventType2

template: "Generate a %s protobuf in the protos/events directory that represents a %s event. It should follow the
           standards of the repo and use buf where possible to enforce requirements. It should be added to the
           PublishEventRequest as event_data. More context exists in the llms.md file."
```

By default `gemini` is used as the agent with the `-i` argument, but that is overrideable with

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
