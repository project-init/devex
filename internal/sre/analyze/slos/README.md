# SLOs

Rank a service's RPCs by how close they run to their response time SLO, so a team can see
which endpoints to fix, or which SLOs to move, before a monitor pages.

`devex sre analyze slos --group <team>` reads each RPC's SLO from the SLO file the protos module
ships, at the version this repository's `go.mod` requires. It then asks Datadog for each RPC's
p95, median, request count, and failure count over the window, in buckets the size of `--rollup`,
and gives each RPC a verdict and an action.

Nothing has to be set up in the repository it runs in. Export the two Datadog keys, pass
`--group`, and the report comes out:

```shell
export DD_API_KEY=... DD_APP_KEY=...
devex sre analyze slos --group gigapi
```

## Usage

```shell
# Score the last 30 days and print the RPCs that need a look.
devex sre analyze slos --group gigapi

# Score the p0 and p1 RPCs from a rollout date, as a self-contained HTML page.
devex sre analyze slos --group gigapi --tag p0,p1 --since 2026-11-03 --format html --out slo-risk.html

# Print the Datadog queries and exit.
devex sre analyze slos --group gigapi --dry-run
```

`--group` without a value, or with one the file does not hold, lists the groups to choose from.

### Flags

| Flag               | Effect                                                                      |
| ------------------ | --------------------------------------------------------------------------- |
| `--group`          | Team whose RPCs to score, as a `services` key of the SLO file               |
| `--tag`            | Score only the RPCs carrying one of these `x-slo-tags`, e.g. `p0,p1`        |
| `--metric-prefix`  | Pin the metric namespace instead of reading it from Datadog                 |
| `--module`         | Read the SLO file from this module instead of finding it from `go.mod`      |
| `--protos-version` | Read the SLO file from this release instead of the one `go.mod` requires    |
| `--days`           | Window to score, in days back from now (default 30)                         |
| `--since`          | Score from this UTC date (`YYYY-MM-DD`) instead of `--days`                 |
| `--rollup`         | Bucket size (default `15m`); match it to the response time monitors' window |
| `--format`         | `table` (default), `md`, `csv`, or `html`                                   |
| `--out`            | Write the report to this file, only once the run succeeds                   |
| `--all`            | Also list HEALTHY, NO DATA, and quiet LOW DATA rows; csv and html always do |
| `--wide`           | Show every column in the table and markdown; csv and html always do         |
| `--batch`          | RPCs per Datadog request (default 8); each RPC costs five queries           |
| `--min-requests`   | Requests in the window below which a row is LOW DATA (default 100)          |
| `--dry-run`        | Print the queries and exit                                                  |

## Which app serves each RPC

Each RPC's metric is `<prefix>.<metric>.response_time`, where the prefix is the app that serves
it. The protos cannot say what that is: the `entities` RPCs are served by the `users` app, and one
group's RPCs can come from several apps. So devex lists the metrics Datadog has seen in the last
30 days and reads the prefix off the metric name, in one request per run. The `APP` column names
it, and an RPC no app reports reads `NO DATA` without a query. `--metric-prefix` pins it instead.

## Configuration

Every field is optional and overrides a default, in `.sre/slos.yaml`:

```yaml
slos:
  module: "" # found from go.mod when empty
  version: "" # used only when go.mod does not require module
  file: sre/configuration/slos.gen.yaml # the default
  group: gigapi # what --group overrides
  metricPrefix: "" # pins the metric namespace; read from Datadog when empty
  datadog:
    site: datadoghq.com # the default
    scope: environment:production # the default; the same metric carries uat
```

### The SLO file

The protos module is any module that ships `sre/configuration/slos.gen.yaml`. devex finds it
among the modules `go.mod` requires, by looking for that file in each one's local `replace`
directory or module cache entry, so it asks the network nothing. When none ships it at the
version `go.mod` requires, run `go mod download` or bump the module; when more than one does, or
there is no `go.mod`, pass `--module`. `--module`, then `module`, skip the search.

`module` is resolved the way Go builds the repository: from the `go.mod` `require`, through any
`replace`. A replace to a local path reads the file from that directory, so a module branch can
be tested before it is released; a pseudo-version works the same as a tag. When `go.mod` does not
require the module, or there is no `go.mod` at all, devex reads the latest release, so a repository
that is not a Go consumer of the protos still gets a report. `--protos-version` overrides both.

devex downloads the module with `go mod download`, so it uses your `GOPROXY`, `GOPRIVATE`, and
credentials, and `go` must be on `PATH`. When the release has no SLO file, the error names the
latest release if that one has it.

The file must be version 1:

```yaml
version: 1
services:
  orders:
    - method: /acme.services.orders.v1.OrdersService/GetOrder
      metric: acmeServicesOrdersV1OrdersServiceGetOrder
      slo: 500 # milliseconds, or null when the RPC has none
      tags: [p0] # what --tag filters on
      evaluationWindow: 1h # this RPC's bucket size, instead of --rollup
  shipping: [] # a group that owns no service; --group shipping says so
```

devex reads `version` first and refuses any other version, rather than guess at its shape. A
field it does not know is ignored. See [testdata/slos.gen.yaml](testdata/slos.gen.yaml).

### Datadog keys

Export `DD_API_KEY` and `DD_APP_KEY`. The app key needs the `timeseries_query` scope. devex does
not fetch them: a key in the shell is one thing to keep working, where a secret to read is an AWS
profile, a session, and a role as well.

## Verdicts

A request is over the SLO when it took the SLO or longer, and near it at 75% of the SLO or more.
Datadog keeps only each bucket's median, p95, and max, so devex estimates each bucket's share
from them: half its requests took the median or longer, 1 in 20 the p95, and none more than the
max, spread evenly in between. Shares weight each bucket by its requests. Exact counts need the
service to record each request's latency range.

The typical p95 is the median of the buckets' p95, weighted by requests: half the requests were
served in a bucket that slow or slower. A monitor already pages on each bucket over the SLO, so the
verdicts ask how the typical bucket runs, not whether one ever crossed.

| Verdict   | When                                                                                           |
| --------- | ---------------------------------------------------------------------------------------------- |
| BREACHING | the typical p95 is at or over the SLO, or 5% of requests or more are over it                   |
| AT RISK   | the typical p95 is 75% of the SLO or more, or a daily p95 trend reaches the SLO within 30 days |
| WATCH     | 5% of requests or more near the SLO                                                            |
| HEALTHY   | none of the above                                                                              |
| NO SLO    | the RPC has no SLO; the action suggests one                                                    |
| LOW DATA  | fewer than `--min-requests` requests; the HTML still shows what the numbers read               |
| NO DATA   | no app reports the metric, or no traffic in the window                                         |

The report sorts the most urgent first. `<75% / 75-100% / >=SLO` splits each RPC's requests by how
long they took against the SLO; CSV gives each share its own column. `TO SLO` is when the daily p95
trend reaches the SLO, left empty when it falls, holds, or gets there past 90 days.

The HTML page has a tile per verdict and a section per verdict, with HEALTHY, LOW DATA, and NO
DATA folded. It shows the default columns with a toggle for the rest, draws the split as a bar that counts the requests in each share on hover,
colors each number red at or over the SLO and yellow at 75% of it or more, and explains each
verdict and column on hover.
