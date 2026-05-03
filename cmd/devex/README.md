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

Run `devex --help` to see all available subcommands, or `devex <subcommand> --help` for detailed help on a specific command.

---

## Subcommands

### SRE

Site Reliability Engineering toolbox for common operational tasks.

**Available Tools:**

- [keygen](../../internal/sre/keygen/README.md) - Generate API keys based on configuration
- [postgres](../../internal/sre/postgres/README.md) - PostgreSQL operations and access management
- [release](../../internal/sre/release/README.md) - Git tag and release management
- analyze - Code analysis operations
- echo - Print and transform arguments

**Usage:**

```shell
devex sre <tool> [args]
```

**Configuration:**

Create a `.sre` directory in your project root to store configuration files. The tool looks for this directory by default, but you can override it with the `--configDir` flag.

**Additional Dependencies:**

Some SRE tools require AWS CLI:

```toml
[tools]
awscli = "latest"  # Required for some postgres operations
```

**Examples:**

```shell
# Generate an API key
devex sre keygen

# Access a postgres database
devex sre postgres access

# Create a new release
devex sre release
```

---

### Workplan

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

A lightweight, opinionated contribution signal generator for GitHub-based engineering teams. Analyzes PR and review activity over configurable time windows (10 / 30 / 90 days) and produces structured output for evaluating contribution patterns.

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

For design intent and cultural philosophy, see [PHILOSOPHY.md](contributions/PHILOSOPHY.md)

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
user,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,num_prs,num_reviews,TotalTimeToMerge,average_days_to_merge
user1,107.98,73.58,34.40,0.68,0.32,74,172,724538000000000,0.11
user2,49.21,39.61,9.60,0.80,0.20,42,48,4127456000000000,1.14
```

Repository signal output:

```csv
repo,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,num_prs,num_reviews,TotalTimeToMerge,average_days_to_merge
business-platform,72.69,45.09,27.60,0.62,0.38,48,138,5021492000000000,1.21
admin-platform,48.03,39.63,8.40,0.83,0.17,40,42,637432000000000,0.18
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
devex components .components
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
