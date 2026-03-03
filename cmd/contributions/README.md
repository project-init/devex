# Contributions

A lightweight, opinionated contribution signal generator for GitHub-based engineering teams. This tool analyzes PR and
review activity over configurable time windows (10 / 30 / 90 days) and produces structured output for evaluating
contribution patterns, merge velocity, and participation balance.

It is designed to be transparent, extensible, and easy to integrate into dashboards or internal reporting systems.

## Overview

The DevEx Contribution Signal provides:

- PR authorship counts
- PR review counts
- PR-to-review ratios
- Total merge time
- Average time-to-merge
- Weighted contribution scoring
- Share breakdowns across contributors

The goal is to make contribution patterns visible and measurable over rolling time windows.

This is **NOT** a replacement for leadership judgment.
It **IS** a visibility tool.

For design intent and cultural philosophy, see:
👉 [PHILOSOPHY.md](./PHILOSOPHY.md)

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/contributions" = "latest"
```

Then you can run the cmd to collect pr information via

```shell
contributions collect contributions_config.yaml
```

to generate user signal output run

```shell
contributions signal contributions_config.yaml
```

to generate user signal output like

```csv
user,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,num_prs,num_reviews,TotalTimeToMerge,average_days_to_merge
user1,107.9807071759259,73.58070717592595,34.39999999999995,0.6814245720399452,0.3185754279600547,74,172,724538000000000,0.11332238488488489
user2,49.21142592592594,39.61142592592594,9.599999999999998,0.8049233522627425,0.19507664773725755,42,48,4127456000000000,1.137416225749559
user3,33.95246296296296,30.75246296296296,3.2000000000000006,0.905750578286744,0.09424942171325597,32,16,2155744000000000,0.7797106481481482
```

and repo signal output like

```csv
repo,weighted_total,weighted_prs,weighted_reviews,weighted_pr_share,weighted_review_share,num_prs,num_reviews,TotalTimeToMerge,average_days_to_merge
business-platform,72.69404398148143,45.09404398148149,27.599999999999934,0.6203265289927886,0.3796734710072113,48,138,5021492000000000,1.2108150077160496
admin-platform,48.03111574074073,39.63111574074073,8.400000000000002,0.8251133693137387,0.17488663068626142,40,42,637432000000000,0.1844421296296296
data-platform,33.949771990740736,23.149771990740742,10.799999999999994,0.681882988700321,0.3181170112996789,24,54,1469194000000000,0.7085233410493826
```

These are easily usable in a spreadsheet, or with an AI to help get some insights in to the data.