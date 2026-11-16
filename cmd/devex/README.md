# devex

Developer Experience Tools - A unified CLI for common developer operations.

## Installation

Add the following to your `mise.toml` file:

```toml
[tools]
"github:project-init/devex" = "latest"  # We suggest pinning a version here
```

This will install the pre-compiled binary from GitHub releases.

Then run:

```shell
mise install
```

## Usage

The `devex` CLI provides a unified interface for all developer experience tools:

```shell
devex <subcommand> [options]
```

Run `devex --help` to see all available subcommands, or `devex <subcommand> --help` for detailed help on a specific command. Run `devex version` or `devex --version` to print the installed version.

Press Ctrl-C to cancel a running command; press it again to terminate devex immediately.

### Global flags

Every command accepts these flags, so scripts and CI can run devex without a terminal:

| Flag                      | Effect                                                                                                                                    |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `-o, --output text\|json` | Write JSON to stdout instead of text. A command that has no JSON form rejects `json`.                                                     |
| `-q, --quiet`             | Print only data, warnings, and errors.                                                                                                    |
| `--no-color`              | Turn off color and borders. devex also turns them off when `NO_COLOR` is set, when `TERM=dumb`, and for any stream that isn't a terminal. |
| `-y, --yes`               | Answer yes to every confirmation.                                                                                                         |

devex writes data to stdout and status, warnings, prompts, spinners, and progress to stderr, so `devex ... > file` captures only data. It prompts only when stdin and stderr are terminals. Without one, a command that needs an answer fails and names the flag that supplies it, such as `pass --env`.

---

## Subcommands

### Localize

Manage gotext translations and generate platform localization bundles.

**Usage:**

```shell
devex localize audit
devex localize html
devex localize mobile
devex localize translate
```

The `audit` command checks that every `en-US` message has a translation and that each locale's
`messages.gotext.json` holds every message gotext extracted into `out.gotext.json`. It prints
findings to stdout and exits non-zero when it finds any.

The `translate` command fills each locale's missing or empty messages with Gemini. It compares
every locale's `messages.gotext.json` with `en-US`, sends the gaps in batches through Google's
Gemini API with the rubric at `localize.rubricPath` as instructions, and writes the translations
back without touching existing entries. It rejects and reports any translation whose
placeholders, such as `{Name}`, differ from the source. Empty `en-US` translations take their
source text, so `localize audit` passes afterward. It skips plural and select messages.

```shell
export GEMINI_API_KEY=...   # https://aistudio.google.com/apikey

# Fill every locale.
devex localize translate

# Preview Spanish and French without writing.
devex localize translate --locale es-US,fr-FR --dry-run

# Machine-readable results.
devex localize translate --output json
```

The `mobile` command generates localization bundles for the platforms present in its
configuration. The currently supported iOS generator distills registered strings from each
`out.gotext.json` catalog into an `l10n-<locale>.json` bundle. It also validates registry
coverage, placeholders, and Swift translation-key usage before replacing existing bundles.

The `html` command reads each locale's curated `messages.gotext.json` catalog and writes a
single combined HTML report to `localize.html.outputPath`, with one column per locale and one
row per message id, so English source strings and their translations can be reviewed side by
side in a browser. The report includes the full set of ids found across all catalogs, including
new English ids that do not yet exist in another locale; their missing translations appear as
highlighted cells. A tile per locale shows its translation coverage, and a search box and a
"Missing only" filter narrow the rows. The report is a single file with inline styles, so it
renders offline.

Configure repository paths under `.localize/*.yaml`:

```yaml
localize:
  localesDir: internal/translations/locales
  rubricPath: .localize/rubric.md
  html:
    outputPath: build/translations.html
  translate:
    model: gemini-2.5-flash # default
    batchSize: 50 # messages per request (default)
    concurrency: 4 # Gemini requests in flight at once (default)
  mobile:
    sourceLanguage: en-US
    registryPath: internal/translations/mobileregistry/registry.go
    ios:
      sourceDir: ios/ProjectInit
      outputDir: ios/ProjectInit/Resources/l10n
```

---

### SRE

Site Reliability Engineering toolbox for common operational tasks.

**Available Tools:**

- [keygen](../../internal/sre/keygen/README.md) - Generate API keys based on configuration
- [postgres](../../internal/sre/postgres/README.md) - PostgreSQL operations and access management
- [release](../../internal/sre/release/README.md) - Git tag and release management with a merged-PR list preview
- [dependencies](../../internal/sre/dependencies/README.md) - Upgrade dependencies and keep every Go version pin in step
- analyze - Code analysis operations, including [slos](../../internal/sre/analyze/slos/README.md): score OpenSLO SLOs against Datadog, with an error budget for each

**Usage:**

```shell
devex sre <tool> [args]
```

**Configuration:**

Create a `.sre` directory in your project root to store configuration files. The tool looks for this directory by default, but you can override it with the `--config-dir` flag (`--configDir` still works). devex merges every `*.yaml` file in the directory and rejects unknown keys, naming the file. It skips files with a top-level `targets` key, which hold `devex discovery` configuration.

**Additional Dependencies:**

`postgres access` runs `psql`, so install a PostgreSQL client. The `postgres` tools call AWS through the SDK and read credentials from the standard AWS configuration, so they work without the AWS CLI. To sign in through AWS SSO, run `aws sso login` from the AWS CLI first.

**Examples:**

```shell
# Generate an API key, or capture one in a script
devex sre keygen
KEY=$(devex sre keygen --length 32)

# Access a postgres database
devex sre postgres access staging-readonly

# Create a new release
devex sre release

# Release a patch from CI
devex sre release --bump patch --yes
```

Requires GitHub auth (`gh auth login` or `GITHUB_TOKEN` set in terminal session `export GITHUB_TOKEN=xyz`) and a github.com `origin`. Before you confirm the tag, lists commits on `origin/main` since the latest tag (full first-parent history when nothing is tagged), grouped into features, fixes, and other changes. Squash merges show the pull request title and author; other commits are listed too. The prompt preselects the bump the titles suggest. The new tag points at `origin/main`, and the command then waits for the release workflow (`--no-wait` skips that). `--dry-run` previews without tagging, and `--output json` prints the versions, changes, and workflow results.

#### Dependencies

Upgrade mise tools, Go modules, and Buf dependencies. `--go` moves every Go version pin (mise,
`go.mod`, `go.work`, Dockerfile golang images, `setup-go`, `.go-version`, and `.tool-versions`) to
one version, verifies each new image tag against its registry, and upgrades each module's direct
requirements under exactly that toolchain. Indirect requirements move only as far as the direct
ones need, and a dependency whose new version needs a newer Go stays put, with a warning. `--mise`
bumps every other mise tool and needs mise 2026.1.3 or newer, and `--buf` moves the plugin versions and git input tags in
`buf.gen.yaml`, then regenerates. A policy per tool or pin caps it at `minor` or `patch`, or pins
it. A git input whose repository a `go.mod` requires follows that module's version instead.
`check` fails on drift without network access. A `.sre` directory is optional.

```shell
# Move every Go pin to the latest patch and upgrade modules.
devex sre dependencies upgrade --go

# Upgrade the ecosystems listed in dependencies.upgrade, printing the plan without writing.
devex sre dependencies upgrade --all --dry-run

# Fail when Go pins disagree; run in pull request CI.
devex sre dependencies check
```

```yaml
dependencies:
  upgrade: [mise, go, buf]
  go:
    target: patch # patch | latest
    directive: none # none | minor | exact
  mise:
    policies:
      node: minor # latest | minor | patch | pin
  buf:
    policies:
      https://github.com/acme/protos.git: minor
```

See the [dependencies README](../../internal/sre/dependencies/README.md) for every flag, config
field, and failure mode.

#### SLOs

Score the SLOs in an OpenSLO v1 file against Datadog, each over its own time window, with a verdict,
an error budget, and an action for each. Nothing has to be set up in the repository it runs in:
export the keys and pass the file. An org on a Datadog site other than `datadoghq.com` also sets
`DD_SITE`.

```shell
export DD_API_KEY=... DD_APP_KEY=...

# Score every SLO in the file.
devex sre analyze slos --slo-file slos/orders.yaml

# Write a self-contained HTML report of one service, only once the run succeeds.
devex sre analyze slos --slo-file slos/all.yaml --group orders --format html --out slo-risk.html
```

See the [slos README](../../internal/sre/analyze/slos/README.md) for every flag and verdict.

---

### Discovery

Guide an idea through discovery, produce reviewable Markdown and YAML artifacts, and publish the resulting work breakdown to Jira or GitHub Issues after human review.

Discovery replaces the deprecated `workplan` command.

Install `devex` in the repository where discovery work will happen using the [installation instructions](#installation). The CLI and all generated configuration and artifacts are owned by that consuming project; you don't need a checkout of the devex source repository.

Run the top-level command from a terminal for guided setup. It detects existing AI harness directories, offers to install the companion skill, creates `.sre/discovery.yaml` when requested, and finishes with a readiness report:

```shell
devex discovery
```

The same flow is available explicitly and can safely install missing files without prompts:

```shell
devex discovery setup
devex discovery setup --harness codex --yes
```

You can also install only the companion Agent Skill:

```shell
# Installs for Codex, Claude Code, and Cursor by default.
devex discovery install-skill

# Or install only one harness.
devex discovery install-skill --harness codex
```

The command installs `run-discovery` under `.agents/skills/`, `.claude/skills/`, or `.cursor/skills/`. Commit the installed skill so each harness can discover the workflow in that project. Installation is idempotent: identical files are reported as `UNCHANGED` and are not rewritten. Different files are protected; review them before using `--force` to install an updated bundled version.

**Check prerequisites:**

```shell
# Checks all supported harnesses by default.
devex discovery doctor

# Check only the harnesses used by this project.
devex discovery doctor --harness codex
```

`doctor` is read-only. It validates the project directory, Git metadata, installed skill contents, `.sre/discovery.yaml`, and customized target values. It also reports whether provider credential environment variables are available without printing their values. Results are grouped as passed, flagged, and failed, with failures rendered last and highlighted on a terminal.

At least one selected harness must have the skill installed. Once one is installed, missing additional harness integrations are flagged for awareness rather than treated as failures. Modified installed skills and missing, invalid, or placeholder configuration remain failures. Missing credentials are warnings because they are only required by `publish apply` and `publish plan --sync`. The command exits nonzero only when failures are present.

`doctor --online` also signs in to each configured target. It confirms the credentials work and can see the target's project or repository, and, for Jira, that every issue type `kind_mapping` names exists in the project. An invalid token or a missing issue type is a failure that names the problem. `--output json` prints the checks as JSON; the exit status still reflects failures.

**Create and validate a bundle:**

```shell
devex discovery init docs/discoveries audit-logs
devex discovery validate docs/discoveries/audit-logs
```

The generated bundle contains:

```text
docs/discoveries/audit-logs/
├── .gitignore
├── discovery.md
└── work-breakdown.yaml
```

`discovery.md` is the narrative document for GitHub-based peer review. `work-breakdown.yaml` is a provider-neutral graph with stable work-item IDs, hierarchy, dependencies, acceptance criteria, labels, and estimates. The generated YAML includes every supported work-item field, using empty lists where appropriate so teams can see what is configurable without consulting the Go schema.

**Configure project defaults and publication targets:**

Generate `.sre/discovery.yaml` in the consuming project and adjust its named targets:

```shell
devex discovery config init
```

The example is also available at [`cmd/devex/discovery/example_config.yaml`](discovery/example_config.yaml) for contributors to devex. Project configuration files contain no credentials. Use `--config <path>` on `init` or `publish plan` when a project needs a different location.

Set credentials only for the provider being applied:

```shell
# GitHub: `gh auth login`, or
export GITHUB_TOKEN=...

# Jira
export JIRA_EMAIL=...
export JIRA_API_KEY=...
```

The Jira base URL and project key live in the target configuration. GitHub targets identify an owner and repository. Set the top-level `default_target` to the name of the target normally used by the project:

```yaml
default_target: github-project
default_labels:
  - discovery
  - team-platform
```

`default_labels` is optional. `init` copies each configured label onto every starter work item in the generated breakdown; the discovery skill preserves those labels as it replaces or adds work items. Labels added directly to an item remain supported alongside the defaults.

`publish plan` resolves its target in this order: an explicit `--target`, `default_target`, or the only configured target. If multiple targets remain and no default is configured, it exits with an error listing the available targets. This keeps scripted overrides explicit while allowing the common path to omit `--target`.

**Plan and apply publication:**

```shell
devex discovery publish plan docs/discoveries/audit-logs
# Override the configured default when needed:
devex discovery publish plan docs/discoveries/audit-logs --target jira-project
devex discovery publish apply docs/discoveries/audit-logs/.publish/github-project/plan.yaml
```

`plan` performs no remote mutations; with `--sync` it reads the target but still writes nothing. It writes a frozen plan and lists its operations grouped by action, with counts, and each update's fields with their before and after values. Warnings go to stderr. `apply` executes that plan and atomically updates `.publish/<target>/receipt.yaml` after each operation. It reports each operation with an `[n/N]` counter, including failed ones and those an earlier run already finished, and ends with a count of created, updated, reused, and failed operations. On Jira, `apply` first checks that the project has every issue type the plan creates, so a bad `kind_mapping` fails before anything is created. `apply` runs operations whose dependencies have finished side by side: up to four at a time on Jira, and one at a time on GitHub, which asks clients to create content serially. After a failure, it lets running operations finish and starts no more; a rerun resumes from the receipt without duplicates.

On GitHub, every issue devex creates carries a `discovery:<id>` label, so `apply` and `plan --sync` list only the discovery's issues instead of the whole repository. The label's description records that it covers every issue. Until it does, as for a new discovery or one published before devex added the label, `apply` scans the repository once, labels the issues it finds, and then records the description; an interrupted run scans again. `plan --sync` writes nothing, so it scans until an `apply` has done that. GitHub keeps labels on new issues only for users with write access, so `apply` stops before creating issues when GitHub reports the token lacks it. Leave the label on the discovery's issues: a re-plan misses an unlabeled issue and creates it again.

When `apply` finds that its plan's receipt records another plan, as after re-planning an edited bundle, it moves the receipt into an `archive/` directory beside the plan and starts a fresh one. It still reuses everything the old receipt recorded, which it finds remotely. `plan`, `apply`, `validate`, and `doctor` accept `--output json`.

Planning without `--sync` is reproducible: an unchanged bundle and target produce the same operations and the same `plan_digest` on any machine. Only `generated_at` records the run, and it is excluded from the digest. `apply` never re-plans; it executes the frozen artifact and refuses to run against a bundle that changed after the plan was written.

**Republish a changed bundle:**

```shell
devex discovery publish plan docs/discoveries/audit-logs --sync
devex discovery publish apply docs/discoveries/audit-logs/.publish/jira-project/sync-<timestamp>/plan.yaml
```

A plain plan only creates: `apply` reuses any issue already published and changes nothing on it. `--sync` reads the target and plans the changes that bring published work in line with the bundle. It creates missing items, keeps unchanged ones, updates changed ones, and names the fields each update rewrites. On Jira it also adds missing links and removes dependency links between the discovery's own issues that the bundle does not declare, including ones added by hand in Jira; the plan lists each removal. A Jira update cannot change an issue's type or remove its parent, so the plan warns instead. On GitHub, dependencies live in the issue body, so a changed dependency surfaces as a body update. A sync never deletes an issue, never removes a label, and never touches links to issues outside the discovery; it warns about published issues the bundle no longer lists.

Each write records a stamp: one digest per bundle field it wrote from, and one of the title and description as the target stored them. Jira keeps the stamp in the issue property beside the idempotency marker; GitHub keeps it in a hidden body comment. The stamp lets a sync tell bundle changes from edits made in Jira or on GitHub: a sync skips and warns about an issue edited since devex last wrote it unless the plan uses `--force`, and `apply` re-checks before writing, so an edit made after the plan stops it even under `--force`. On Jira, label and parent changes leave edits intact, so they proceed without `--force`, and forcing over an edit rewrites both the summary and the description. On GitHub, every update rewrites the title, body, and labels. Issues published before stamps existed carry none, so the first sync rewrites each of them once and records one. A write devex could not read back, or a stamp damaged by hand, counts as edited, because devex cannot rule out a later edit.

`--sync` needs the provider credentials `apply` needs, and a sync plan reflects the remote as it stood when planned. It writes to `.publish/<target>/sync-<timestamp>/`, so each sync plan keeps its own receipt.

The generated `.gitignore` excludes `.publish/`. Receipts are small local YAML files that allow a later CLI session to resume an interrupted publication. Jira issue properties and hidden GitHub issue-body markers provide a secondary idempotency check if a receipt is lost.

The tool deliberately does not prove or verify peer approval. Review and approval happen through the team's normal GitHub process; a human explicitly runs `publish apply` afterward.

GitHub initiatives are tracking issues, and hierarchy and dependencies are rendered as links in issue bodies. Jira kind and issue-type mappings are configured per target. Jira publishes every `depends_on` edge as a real issue link oriented so the dependency blocks the dependent item; set `link_type` on the target to use a link type other than `Blocks`.

Write the discovery document's title as a Markdown link to the issue that prompted the investigation, as the template does. Jira relates every published epic back to that issue, and GitHub links it from the tracking issue's body, so a reader who lands there can reach the request behind it. A title linking another tracker is reported as a Jira warning and skipped, because only an issue in the target instance can be linked.

Item descriptions and acceptance criteria are GitHub Flavored Markdown. GitHub renders them directly; Jira converts them to Atlassian Document Format, so headings, lists, tables, task lists, code, emphasis, strikethrough, and links survive rather than flattening into prose. Raw HTML has no ADF equivalent and is dropped, and an image becomes a link to its source because ADF images reference uploaded attachments.

Published issues footer the discovery document as a URL on the repository's default branch, resolved from the `origin` remote. The link works once the branch carrying the bundle merges. A bundle outside a GitHub checkout has no such URL, and the footer is omitted rather than naming a path no reader can open.

---

### Workplan

> **Deprecated:** use [`devex discovery init`](#discovery) and `devex discovery publish` instead. A future release removes `workplan`.

Generate and publish workplan templates for investigating features or problems. Workplans help estimate the effort needed to complete a task and can be published directly to JIRA.

**Usage:**

```shell
devex workplan generate <directory> <title>
devex workplan publish <workplan_path>
```

**Generate Example:**

```shell
devex workplan generate docs/investigations/infrastructure/moveToProjectInitStack example_title
```

This will generate a workplan template in the specified directory:

```
<directory>
└── <yyyy>_<m>_<d>_example_title
    ├── workplan.yaml
    └── problem.md
```

**Publishing to JIRA:**

Set the following environment variables (consider adding them to your shell profile):

```shell
export JIRA_URL=https://yourdomain.atlassian.net
export JIRA_EMAIL=your_jira_email
export JIRA_API_KEY=your_api_key
```

Then publish:

```shell
devex workplan publish docs/investigations/infrastructure/moveToProjectInitStack/2026_5_3_example_title/workplan.yaml
```

**Getting Your JIRA API Key:**

1. Navigate to https://id.atlassian.com/manage-profile/security/api-tokens
2. Click "Create API token"
3. Give it a name and click "Create"
4. Copy the token and set it in your environment variables

**Why JIRA?**

JIRA is the most common work tracking tool used by the project-init team. While we don't claim it's the best tool, it's what we have expertise with and have built integration for.

---

### Contributions

A lightweight, opinionated contribution signal generator for GitHub-based engineering teams. Analyzes PR and review activity over the look-back window set by `numLookBackDays` and produces structured output for evaluating contribution patterns.

**What It Provides:**

- PR authorship counts
- PR review counts
- PR-to-review ratios
- Total merge time
- Average time-to-merge
- Weighted contribution scoring
- Share breakdowns across contributors and repositories

**Philosophy:**

This is **NOT** a replacement for leadership judgment.  
It **IS** a visibility tool.

**Usage:**

```shell
# Collect PR data
devex contributions collect <config_file>

# Generate signal output
devex contributions signal <config_file>
```

**Example:**

```shell
devex contributions collect contributions_config.yaml
devex contributions signal contributions_config.yaml
```

**Output:**

User signal output:

```csv
user,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,weighted_distribution_modifier,num_prs,num_reviews,average_days_to_merge
user1,107.98172,73.58172,34.4,0.6814287,0.3185713,1,74,172,0.1133184
user2,49.2104,39.6104,9.6,0.8049193,0.1950807,1,42,48,1.1373741
```

Repository signal output:

```csv
repo,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,num_prs,num_reviews,num_contributors,average_days_to_merge
business-platform,72.6928,45.0928,27.6,0.6203197,0.3796803,48,138,6,1.2108122
admin-platform,48.0317,39.6317,8.4,0.8251176,0.1748824,40,42,4,0.1844425
```

These CSV outputs are easily imported into spreadsheets or analyzed with AI tools.

**Configuration:**

See [example_config.yaml](contributions/example_config.yaml) for a sample configuration file.

---

### Components

Generate component skeleton code from YAML configuration files. Currently focused on database components for PostgreSQL-based Go services.

**Usage:**

```shell
devex components <configuration_file_path>
```

**Example:**

```shell
devex components cmd/devex/components/example_config.yaml
```

**Configuration Example:**

```yaml
outputDirectory: "gen"

db:
  schemaName: data_platform
```

See [example_config.yaml](components/example_config.yaml) for a complete example.

**DB Components:**

The DB component generator outputs:

- User setup scripts
- Schema definitions
- IAM permissions setup
- Default migrations
- Post-release scripts
- sqlc configuration for Go service integration

All setup is optimized for PostgreSQL clusters.

---

## Additional Resources

- [Code of Conduct](../../CODE_OF_CONDUCT.md)
- [Contribution Guide](../../CONTRIBUTING.md)

## Development

Run tests:

```shell
mise test
```

Build locally:

```shell
go build -o devex ./cmd/devex
```

Run linting:

```shell
mise lint
```
