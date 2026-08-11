---
name: run-discovery
description: Turn an idea into a reviewable product or technical discovery bundle containing discovery.md and work-breakdown.yaml. Use when exploring a feature, problem, architectural change, or uncertain initiative; refining intent and requirements; comparing approaches; writing a discovery document; creating a structured implementation breakdown; or preparing approved discovery work for Jira or GitHub Issues.
---

# Run Discovery

Guide the user from an early idea to a validated discovery bundle. Keep reasoning adaptive and conversational; use the `devex discovery` CLI for deterministic file generation, validation, and publication.

Announce: "I'm using the run-discovery skill to understand your intent and produce reviewable discovery artifacts."

## Check the project setup

1. Run `devex discovery --help` to verify that the project-installed CLI is available. If it is missing, stop and direct the user to install `github:project-init/devex` with their project tool manager.
2. Treat discovery artifacts and configuration as files owned by the consuming project, never by the devex source repository.
3. Run `devex discovery doctor --harness <active-harness>` before creating artifacts. Resolve missing or modified skill files and invalid configuration. Credential warnings may wait until publication.
4. When setup is incomplete, offer to run `devex discovery setup --harness <active-harness>`. Have the user customize target values generated in `.sre/discovery.yaml`. Never add credentials to that file.

## Discover

1. Inspect the current project, relevant documentation, and recent changes before asking design questions.
2. Use parallel workers for independent context gathering when the active harness supports them and parallelism will materially help.
3. Assess whether the idea is rough, partially formed, or implementation-ready. Read [references/discovery-playbook.md](references/discovery-playbook.md) for the readiness rubric.
4. Ask one question per turn. Prefer two or three concrete options with a recommended option first when the choice can be bounded.
5. Focus on purpose, constraints, evidence, success criteria, ownership, and consequential unknowns. Avoid speculative rabbit holes.
6. Label statements as known, assumed, or unknown when their status matters.

## Surface every fork and every correction

Later passes routinely overturn earlier ones. Take each reversal back to the user; never absorb it silently.

1. Ask any decision that appears after the first question round. A fork found late is still the user's to make, and an early question round buys no license to decide the rest alone.
2. When evidence contradicts an assessment you already gave, say so plainly, state what it changes, and re-ask every question whose premise it invalidated. Answers collected under a false premise are void.
3. Never park a decision in the bundle or a pull request body as an "open question" instead of asking it. That shifts the choice onto whoever reads the document and buries it where it is easy to miss.
4. Record in **Unknowns** only what the user explicitly deferred, or what the breakdown schedules as research. Unknowns is not an inbox for decisions you declined to raise.
5. Verify a claim before it becomes a premise. Read whole files rather than truncated output, and treat a conclusion drawn from a partial read as unverified.

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

Populate `discovery.md` using the accepted design and populate `work-breakdown.yaml` using [references/work-breakdown.md](references/work-breakdown.md). Read `default_labels` from the discovery configuration (`.sre/discovery.yaml` unless `--config` selected another file) and include every configured default on every work item, merging them with item-specific labels without duplicates. The CLI seeds its starter items with these defaults. If the CLI is unavailable, use the templates in `assets/` as fallbacks and apply the defaults manually.

Keep narrative reasoning in `discovery.md`. Keep executable scope, hierarchy, dependencies, acceptance criteria, and estimates in `work-breakdown.yaml`; do not duplicate the entire discovery narrative in every work item.

Write every word a human will read — `discovery.md`, `work-breakdown.yaml`, and the pull request body that carries them — to [references/prose-style.md](references/prose-style.md).

Run:

```bash
devex discovery validate <bundle-directory>
```

Resolve every validation error before presenting the bundle for peer review. Tell the user to review the Markdown and YAML through their normal GitHub workflow.

## Publish after human review

Do not inspect, prove, or claim peer approval. Do not publish unless the user explicitly says human review is complete and asks to publish.

Create and show the read-only plan first:

```bash
devex discovery publish plan <bundle-directory> [--target <target>]
```

The CLI uses an explicit `--target`, then `default_target`, then the sole configured target. If multiple targets remain ambiguous, ask the user which target to use. Explain every warning or lossy provider mapping. Only run the explicit apply command after the user confirms the displayed plan:

```bash
devex discovery publish apply <plan-file>
```

Report the receipt path and created or reused remote work items.
