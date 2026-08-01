---
name: run-discovery
description: Turn an idea into a reviewable product or technical discovery bundle containing discovery.md and work-breakdown.yaml. Use when exploring a feature, problem, architectural change, or uncertain initiative; refining intent and requirements; comparing approaches; writing a discovery document; creating a structured implementation breakdown; or preparing approved discovery work for Jira or GitHub Issues.
---

# Run Discovery

Guide the user from an early idea to a validated discovery bundle. Keep reasoning adaptive and conversational; use the `devex discovery` CLI for deterministic file generation, validation, and publication.

Announce: "I'm using the run-discovery skill to understand your intent and produce reviewable discovery artifacts."

## Discover

1. Inspect the current project, relevant documentation, and recent changes before asking design questions.
2. Use parallel workers for independent context gathering when the active harness supports them and parallelism will materially help.
3. Assess whether the idea is rough, partially formed, or implementation-ready. Read [references/discovery-playbook.md](references/discovery-playbook.md) for the readiness rubric.
4. Ask one question per turn. Prefer two or three concrete options with a recommended option first when the choice can be bounded.
5. Focus on purpose, constraints, evidence, success criteria, ownership, and consequential unknowns. Avoid speculative rabbit holes.
6. Label statements as known, assumed, or unknown when their status matters.

## Compare approaches

1. Present two or three viable approaches with trade-offs.
2. Lead with the recommended approach and explain why it best fits the discovered constraints.
3. Apply YAGNI: remove optional systems, integrations, and abstractions that are not required to validate the idea.

## Validate the design

Present the design in sections of at most 300 words. Validate each section with the user before continuing. Cover architecture, components, data flow, failure handling, security or access concerns, rollout or phasing, and testing when applicable.

Do not create final artifacts until the important design sections are accepted.

## Create the bundle

Run:

```bash
devex discovery init <directory> <name>
```

Populate `discovery.md` using the accepted design and populate `work-breakdown.yaml` using [references/work-breakdown.md](references/work-breakdown.md). If the CLI is unavailable, use the templates in `assets/` as fallbacks.

Keep narrative reasoning in `discovery.md`. Keep executable scope, hierarchy, dependencies, acceptance criteria, and estimates in `work-breakdown.yaml`; do not duplicate the entire discovery narrative in every work item.

Run:

```bash
devex discovery validate <bundle-directory>
```

Resolve every validation error before presenting the bundle for peer review. Tell the user to review the Markdown and YAML through their normal GitHub workflow.

## Publish after human review

Do not inspect, prove, or claim peer approval. Do not publish unless the user explicitly says human review is complete and asks to publish.

Create and show the read-only plan first:

```bash
devex discovery publish plan <bundle-directory> --target <target>
```

Explain every warning or lossy provider mapping. Only run the explicit apply command after the user confirms the displayed plan:

```bash
devex discovery publish apply <plan-file>
```

Report the receipt path and created or reused remote work items.
