# Contributions

The contributions cmd is meant to generate a signal file which can be used to determine which devs/repos are having the
most impact or potentially are having issues. Can be used on a repo level basis, but the suggestion is to make a repo
such as `github.com/yourorg/contributions` where you run a nightly cron to collect data and create signal from it.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
"go:github.com/project-init/devex/cmd/contributions" = "latest"
```

Then you can run the cmd like

```shell
contributions contributions_config.yaml
```

to generate output like

```csv
user,repo,num_prs,num_reviews,weighted_prs,weighted_reviews,weighted_total,average_days_to_merge
user1,,43,102,42.77784895833334,20.39999999999996,63.1778489583333,0.10332606589147286
user2,,23,7,22.14840625,1.4,23.54840625,0.740516304347826
user3,,20,10,19.41407986111111,1.9999999999999998,21.41407986111111,0.5859201388888889
```

and

```csv
user,repo,num_prs,num_reviews,weighted_prs,weighted_reviews,weighted_total,average_days_to_merge
,tommon,7,9,6.990792245370371,1.7999999999999998,8.79079224537037,0.02630787037037037
,gommon,3,9,2.6162690972222222,1.7999999999999998,4.416269097222222,2.5582060185185185
,terraform-aws-rds,1,5,0.9478078703703704,1,1.9478078703703705,1.0438425925925927
,terraform-aws-api-service,1,3,0.9735729166666667,0.6000000000000001,1.5735729166666668,0.5285416666666667
,terraform-aws-grpc-service,1,3,0.9735491898148149,0.6000000000000001,1.5735491898148148,0.5290162037037037
,devex,1,1,0.998953125,0.2,1.198953125,0.020937499999999998
,terraform-aws-internal-service,1,0,0.9995526620370371,0,0.9995526620370371,0.00894675925925926
```
