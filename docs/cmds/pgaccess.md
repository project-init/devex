# PGAccess

The `pgaccess` cmd is meant to use as a way to connect to postgres clusters either locally or via rds connect (AWS). It
wraps `psql`, so there is an explicit dependency on that being installed. `psql` comes with any postgres installation so
it should be easy enough to add as an installation via `brew` or your favorite package management software.

## Setup

Add the following to your `mise.toml` file

```toml
[tools]
awscli = "latest"
"go:github.com/project-init/devex/cmd/pgaccess" = "latest"
```

Then you can run the cmd like

```shell
pgaccess <pgaccessfile> <environment>
```

which will use set up the psql command to run as that user in the given environment assuming the proper definition has
been established in the `.pgaccess` file.

**NOTE**: You will also need `psql` installed, which comes with any installation of `postgres`. At time of writing this
doc, postgres doesn't seem to install via mise on Mac. Instead, the suggested install path for `postgresql` is via brew.

```yaml
environments:
  localhost-readonly:
    host: "localhost"
    database: "database"
    port: 5432
    sslMode: "disable"
    username: "readonly"
    password: ""
  staging-readonly:
    # Can be retrieved via
    # aws rds describe-db-cluster-endpoints --db-cluster-identifier <identifier> --filters "Name=db-cluster-endpoint-type,Values=< reader | writer >" --query 'DBClusterEndpoints[].Endpoint' --output text --region us-east-1
    host: "your-host.rds.amazonaws.com"
    database: "database"
    port: 5432
    sslMode: require
    username: "readonly"
    iam: true
```

## Why psql as the default?

The `psql` CLI comes with an installation of postgresql by default, so it makes it very likely to exist on any system
trying to use this command. It also is performant and well maintained.

## FAQ

**Q:** I'm having trouble accessing my rds DB, what should I do next?
**A:** First confirm you have your AWS_PROFILE set correctly, and you have done an `aws sso login` (or whatever form of
login you do). Then double-check the config. When in doubt, confirm you can log in via psql on your local terminal. As
this is a wrapper, generally the issue lies in config/login rather than the tool itself.