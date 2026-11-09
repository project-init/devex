# Errtrack

Pull the metadata behind a Datadog Error Tracking link down as JSON.

Triaging an error means opening the link, reading the stack trace, finding the service and the
commit, then retyping all of it into a ticket or a prompt. `errtrack` takes the link and emits
that context as structured JSON on stdout. Ready to pipe into a coding agent, or to hand to a
ticket-creation step.

## Usage

```shell
# Print the metadata as JSON.
devex sre errtrack <link>

# A bare issue id works too, when you already have one.
devex sre errtrack aae10c1c-dacd-11f1-a39f-da7ad0900002

# Write it to a file instead of stdout, only once the run succeeds.
devex sre errtrack <link> -o error.json

# Hand it straight to a coding agent.
devex sre errtrack <link> | claude -p "fix this error"
```

Both link shapes Datadog produces are accepted: the full issue page
(`/error-tracking/issue/<id>`) and the explorer with an issue panel open
(`/error-tracking?sp=...`). The explorer one is what you get clicking a row in the list.

| Flag             | Purpose                                                                                                                               |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `-o`, `--out`    | Write to a file instead of stdout. The file is only touched once the run succeeds, so a failure leaves the last good report in place. |
| `--env`          | Record a different environment. One is read from the error itself, so this is only needed when that is missing or wrong.              |
| `--datadog-site` | Override the site. Resolved from the link's own host first, then `DD_SITE`, then `DATADOG_SITE`, then `datadoghq.com`.                |

## Credentials

Set a Datadog API key and application key in the environment. Both are required, and the app key
needs access to Error Tracking:

```shell
export DD_API_KEY=...
export DD_APP_KEY=...
```

Keys only work on their own Datadog site. The site is read from the link, so an org on
`datadoghq.eu` needs nothing extra — and if the link disagrees with `DD_SITE`, `errtrack` says so
on stderr rather than letting it surface as a confusing 403.

DailyPay engineers can pull the keys from AWS Secrets Manager. Access is scoped per role, not per
account:

```shell
export AWS_PROFILE=dailypay:employees:staging:operator
eval "$(aws secretsmanager get-secret-value --secret-id infrastructure-managed-secrets \
  --query SecretString --output text \
  | jq -r '"export DD_API_KEY=\(.DD_API_KEY) DD_APP_KEY=\(.DD_APP_KEY)"')"
```

## Configuration

None. `errtrack` takes everything it needs from the link, the flags and the environment, so it
runs in any repository with or without a `.sre` directory.

#### Description

The link identifies a Datadog Error Tracking issue. `errtrack` resolves it to an issue id, fetches
the issue together with a code-level sample, and writes a JSON document: a `source` field naming
the provider, then the error (type, message, service, environment, state), the timeline with the
application version at first and last sight, the location in the source, any stack frames, and a
link back to the issue.

Every key is always present. Values that are unknown are `null` rather than absent, so a consumer
never has to tell one from the other.

```json
{
  "schema_version": 1,
  "source": "datadog",
  "fetched_at": "2026-11-09T02:08:28Z",
  "summary": "[login-ui-app] *errors.errorString: get identity 2173…: HTTP 503: undefined response type",
  "error": {
    "type": "*errors.errorString",
    "service": "login-ui-app",
    "environment": "production",
    "state": "OPEN"
  },
  "timeline": {
    "first_seen": "2026-11-07T17:07:07.024Z",
    "last_seen_version": "v1.1.14"
  },
  "code": {
    "location": { "file": "/app/pkg/kratos/admin.go", "line": null },
    "frames": null
  },
  "links": {
    "issue": "https://app.datadoghq.com/error-tracking/issue/aae10c1c-…"
  }
}
```

### What may be missing, and why

**Stack frames, the repository and the commit need Source Code Integration.** Datadog treats it as
optional, and without it a sample arrives with an empty `error_stack` and no git fields at all. The
`code` section is then limited to the file and function the issue itself reports — the path as the
build saw it, often with a container prefix, and no line number. Enabling the integration fills
those fields in with no change here.

**Occurrence counts are always `null`.** Datadog serves `total`, `impacted_users` and
`impacted_sessions` only from the issue-search endpoint, never from a single issue lookup, so
reporting a number would mean a second request.

**There is no trace link.** Datadog's trace URL format is not documented in the API spec and has
not been confirmed against a real one, and a link that 404s from a ticket is worse than no link.
The trace and span ids are in the API response if you want to add it after checking the format.
