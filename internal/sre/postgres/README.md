# Postgres

Collection of postgres tools meant to make it easier to manage your production clusters.

## Configuration

```yaml
postgres:
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

## Commands

### access

#### Usage

```shell
sre postgres access
```

#### Description

The access command uses an SRE yaml file (defaults to `.sre`) to determine what the login configuration is for your
given postgres clusters. Assumes you have done any SSO login or similar you might need to have access through your
vendor as well. The `access` tool will open a selection window which after selection will set up the psql command to run
as that user in the given environment assuming the proper definition has been established in the `.sre` file.

**NOTE**: You will also need `psql` installed, which comes with any installation of `postgres`. At time of writing this
doc, postgres doesn't seem to install via mise on Mac. Instead, the suggested install path for `postgresql` is via brew.

### migrate

#### Usage

```shell
sre postgres migrate \
  --cluster <ecs-cluster> \
  --task-def <task-definition> \
  --subnets <subnet-1,subnet-2> \
  --security-groups <sg-1,sg-2> \
  --container <container-name> \
  --image-uri <docker-image-uri> \
  [--region <aws-region>]
```

Each flag also falls back to an environment variable, so the command can be driven entirely from the environment in
CI/CD:

| Flag                | Environment variable | Required | Default     |
| ------------------- | -------------------- | -------- | ----------- |
| `--cluster`         | `CLUSTER`            | yes      |             |
| `--task-def`        | `TASK_DEF`           | yes      |             |
| `--subnets`         | `SUBNETS`            | yes      |             |
| `--security-groups` | `SECURITY_GROUPS`    | yes      |             |
| `--container`       | `CONTAINER`          | yes      |             |
| `--image-uri`       | `IMAGE_URI`          | yes      |             |
| `--region`          | `REGION`             | no       | `us-east-1` |

#### Description

The migrate command runs a database migration as a one-off ECS Fargate task. It takes an existing task definition,
registers a new revision pointing the named container at the provided image, then runs that revision in the given
cluster and waits (up to 30 minutes) for it to complete. On a non-zero exit it pulls the recent CloudWatch logs from
`/ecs/<task-def>` to help diagnose the failure, and exits non-zero so callers (e.g. a deploy pipeline) can fail fast.

**NOTE**: This relies on standard AWS credential resolution, so make sure your `AWS_PROFILE`/SSO login (or CI role) is in
place and has permission to describe/register task definitions and run tasks on the target cluster.

## Why psql as the default?

The `psql` CLI comes with an installation of postgresql by default, so it makes it very likely to exist on any system
trying to use this command. It also is performant and well maintained.

## FAQ

**Q:** I'm having trouble accessing my rds DB, what should I do next?
**A:** First confirm you have your AWS_PROFILE set correctly, and you have done an `aws sso login` (or whatever form of
login you do). Then double-check the config. When in doubt, confirm you can log in via psql on your local terminal. As
this is a wrapper, generally the issue lies in config/login rather than the tool itself.
