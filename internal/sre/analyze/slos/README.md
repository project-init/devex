# SLOs

Score the SLOs in an [OpenSLO v1](https://openslo.com/specification/) file against Datadog, so a team
can see which SLOs to fix, or which to move, before their error budget runs out.

`devex sre analyze slos --slo-file <file>` reads each SLO, asks Datadog for its data over the SLO's
own time window, ending now, and gives each SLO a verdict, an error budget, and an action. A 7d SLO
is scored over the last 7 days, and a 30d one over the last 30.

Nothing has to be set up in the repository it runs in. Export the two Datadog keys, pass the file,
and the report comes out:

```shell
export DD_API_KEY=... DD_APP_KEY=...
devex sre analyze slos --slo-file slos/orders.yaml
```

## Usage

```shell
# Score every SLO in a file of one service, and print the ones that need a look.
devex sre analyze slos --slo-file slos/orders.yaml

# Score the p0 and p1 SLOs of one service, as a self-contained HTML page.
devex sre analyze slos --slo-file slos/all.yaml --group orders --tag p0,p1 --format html --out slo-risk.html

# Reproduce a report: end every window at a fixed time.
devex sre analyze slos --slo-file slos/orders.yaml --at 2026-11-02T00:00:00Z

# Print the Datadog queries and exit. It needs no keys.
devex sre analyze slos --slo-file slos/orders.yaml --dry-run
```

### Flags

| Flag             | Effect                                                                          |
| ---------------- | ------------------------------------------------------------------------------- |
| `--slo-file`     | The OpenSLO v1 file to score (required)                                         |
| `--group`        | Score the SLOs of this `spec.service`; needed when the file holds more than one |
| `--tag`          | Score only the SLOs whose `tags` label has one of these, e.g. `p0,p1`           |
| `--at`           | End every window here instead of now, in RFC 3339                               |
| `--datadog-site` | Datadog site of the org instead of `DD_SITE`, e.g. `datadoghq.eu`               |
| `--format`       | `table` (default), `md`, `csv`, or `html`                                       |
| `--out`          | Write the report to this file, only once the run succeeds                       |
| `--all`          | Also list HEALTHY, NO DATA, and quiet LOW DATA rows; csv and html always do     |
| `--wide`         | Show every column in the table and markdown; csv and html always do             |
| `--batch`        | SLOs per Datadog request (default 8)                                            |
| `--min-events`   | Events below which an Occurrences SLO is LOW DATA (default 100)                 |
| `--dry-run`      | Print the queries and exit                                                      |

## OpenSLO

devex reads this subset of OpenSLO v1, and refuses the rest with what to change:

- `kind: SLO` documents; any other kind, such as `Service`, is skipped.
- An inline `spec.indicator`, not an `indicatorRef`, and one rolling `timeWindow` in `m`, `h`, `d`, or `w`.
- One objective, with `target` as a share, e.g. `0.95`.
- `budgetingMethod: Timeslices` with a `thresholdMetric`, `op: lt`, a `value` above 0 in the
  indicator's own unit, and a `timeSliceWindow`. A slice at the value counts as bad.
- `budgetingMethod: Occurrences` with a `ratioMetric` of `good` and `total`.
- A `metricSource` of `type: Datadog`. Write the query without `.rollup()` or `.as_count()`: devex
  adds the rollup per slice, and counts a ratio's queries.

`metadata.displayName` names the SLO in the report when it is set, and `metadata.name` otherwise.
The `tags` label, a list or a comma-separated string, is what `--tag` filters on. See
[testdata/openslo.yaml](testdata/openslo.yaml). Each document converts into one SLO type right after
parsing, so the scoring reads none of OpenSLO's shape.

### Metrics sources

Each backend is a source that owns its query syntax and returns each SLO's points per slice; the
scoring does the math without knowing which backend it talks to. Datadog is the one source today.
Another backend, such as Prometheus, is one more implementation of `metricSource` in `fetch.go`.

### Datadog keys

Export `DD_API_KEY` and `DD_APP_KEY`. The app key needs the `timeseries_query` scope. devex does not
fetch them: a key in the shell is one thing to keep working, where a secret to read is an AWS
profile, a session, and a role as well.

The keys go to the org's Datadog site: `--datadog-site`, then `DD_SITE`, then `DATADOG_SITE`, then
`datadoghq.com` (US1). An org on US1 sets nothing. An org on another site sets it beside the keys:

```shell
export DD_API_KEY=... DD_APP_KEY=... DD_SITE=datadoghq.eu
```

## Verdicts

Every SLO gets an error budget from its target. A slice under the threshold is good and one at it or
over it is bad; for a ratio, a good event is good. `BURN` is the bad share over the share the target
allows: `1.00x` spends exactly the budget over the window, and `2.00x` spends it in half the window.
`BUDGET LEFT` is what is not spent yet, negative once overspent.

A Timeslices SLO also reads its slices against the threshold. `TYPICAL` is the median slice and
`WORST` the slice 1 in 20 reach. A slice is near the threshold at 75% of it or more.

| Verdict   | When                                                                                              |
| --------- | ------------------------------------------------------------------------------------------------- |
| BREACHING | the error budget is spent: a burn of `1.00x` or more                                              |
| AT RISK   | a burn of `0.75x` or more, or a daily median trend that reaches the threshold in 30 days          |
| WATCH     | the typical slice is at 75% of the threshold or more                                              |
| HEALTHY   | none of the above                                                                                 |
| LOW DATA  | under a day of slices, or under `--min-events` events; the HTML still shows what the numbers read |
| NO DATA   | the queries returned nothing in the window                                                        |

The report sorts the most urgent first. `<75% / 75-100% / >=THRESHOLD` splits each SLO's slices by
where they sat against the threshold; CSV gives each share its own column. `TO THRESHOLD` is when the
daily median trend reaches the threshold, left empty when it falls, holds, or gets there past 90 days.
A healthy SLO whose worst slices stay under half its threshold gets a lower one to consider.

The HTML page has a tile per verdict and a section per verdict, with HEALTHY, LOW DATA, and NO DATA
folded. It shows the default columns with a toggle for the rest, draws the split as a bar that counts
the slices in each share on hover, colors each number red at or over the threshold and yellow at 75%
of it or more, and explains each verdict and column on hover.
