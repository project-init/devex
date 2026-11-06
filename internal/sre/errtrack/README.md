# Errtrack

Pull the metadata behind a Datadog error link down as JSON.

Triaging an error means opening the link, reading the stack trace, finding the service and the
commit, then retyping all of it into a ticket or a prompt. `errtrack` takes the link and emits
that context as structured JSON on stdout. Ready to pipe into a coding agent, or to hand to a
ticket-creation step.

## Usage

```shell
# Print the metadata as JSON.
devex sre errtrack <link>

# Write it to a file instead of stdout.
devex sre errtrack <link> -o error.json

# Hand it straight to a coding agent.
devex sre errtrack <link> | claude -p "fix this error"
```

## Credentials

Set a Datadog API key and application key in the environment. Both are required:

```shell
export DD_API_KEY=...
export DD_APP_KEY=...
```

DailyPay engineers can pull them from AWS Secrets Manager. Access is scoped per role, not per
account:

```shell
export AWS_PROFILE=dailypay:employees:staging:operator
eval "$(aws secretsmanager get-secret-value --secret-id infrastructure-managed-secrets \
  --query SecretString --output text \
  | jq -r '"export DD_API_KEY=\(.DD_API_KEY) DD_APP_KEY=\(.DD_APP_KEY)"')"
```

## Configuration

None. `errtrack` takes everything it needs from the link and the environment, so it runs in any
repository with or without a `.sre` directory.

#### Description

The link identifies a Datadog Error Tracking issue. `errtrack` resolves it to an issue ID and
fetches the issue along with a code-level sample, which carries the parsed stack trace and the
git metadata for the frames. The output is a JSON document with a `source` field naming the
provider, a common core (service, environment, error type and message, first and last seen, a
link back to the original issue), and the provider-specific detail: stack frames with file,
function and line, the repository URL and commit, and deep links to the trace and session replay.
