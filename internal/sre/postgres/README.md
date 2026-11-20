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
      region: us-east-1 # signs the IAM token (default us-east-1)
```

## Commands

### access

#### Usage

```shell
devex sre postgres access staging-readonly
devex sre postgres access --env staging-readonly
devex sre postgres access   # choose from a list on a terminal
```

#### Description

The access command uses an SRE yaml file (defaults to `.sre`) to determine what the login configuration is for your
given postgres clusters. Assumes you have done any SSO login or similar you might need to have access through your
vendor as well. Name the environment as an argument or with `--env`, or omit it to choose from a list on a terminal. The
tool then runs psql as that user in that environment, as defined in the `.sre` file. It passes the connection settings
and the password through libpq's `PG*` environment variables, so `ps` never shows the password. With `iam: true` it
signs an RDS IAM token for the environment's `port` (default 5432) and `region` (default us-east-1). A psql failure
reports its exit status and the environment.

**NOTE**: You will also need `psql` installed, which comes with any installation of `postgres`. At time of writing this
doc, postgres doesn't seem to install via mise on Mac. Instead, the suggested install path for `postgresql` is via brew.

### migrate

#### Usage

```shell
devex sre postgres migrate \
  --cluster <ecs-cluster> \
  --task-def <family | family:revision | arn> \
  --subnets <subnet-1,subnet-2> \
  --security-groups <sg-1,sg-2> \
  --container <container-name> \
  --image-uri <docker-image-uri> \
  [--region <aws-region>] \
  [--launch-type <ecs-launch-type> | --capacity-provider <capacity-provider>] \
  [--wait-timeout <duration>] \
  [--dry-run]
```

Each value flag falls back to a `DEVEX_` environment variable, so CI can drive the command from the environment alone:

| Flag                  | Environment variable      | Required | Default     |
| --------------------- | ------------------------- | -------- | ----------- |
| `--cluster`           | `DEVEX_CLUSTER`           | yes      |             |
| `--task-def`          | `DEVEX_TASK_DEF`          | yes      |             |
| `--subnets`           | `DEVEX_SUBNETS`           | yes      |             |
| `--security-groups`   | `DEVEX_SECURITY_GROUPS`   | yes      |             |
| `--container`         | `DEVEX_CONTAINER`         | yes      |             |
| `--image-uri`         | `DEVEX_IMAGE_URI`         | yes      |             |
| `--region`            | `DEVEX_REGION`            | no       | `us-east-1` |
| `--launch-type`       | `DEVEX_LAUNCH_TYPE`       | no       | `FARGATE`   |
| `--capacity-provider` | `DEVEX_CAPACITY_PROVIDER` | no       |             |
| `--wait-timeout`      |                           | no       | `30m`       |

Earlier releases read the unprefixed names, such as `CLUSTER`. Generic names like `REGION` collide with other tools, so
migrate reads only the `DEVEX_` names. It fails when an old name is set and neither its `DEVEX_` variable nor its flag
replaces it, rather than quietly running with a default.

#### Description

The migrate command runs a database migration as a one-off ECS task. It describes the task definition, registers a new
revision that copies everything except the named container's image and the tags, runs that revision in the cluster, and
waits for it to stop. It exits non-zero when the migration fails, so a deploy pipeline can fail fast.

While the task runs, migrate streams the container's log lines to stdout and reports the task's status on stderr: a live
status and elapsed time on a terminal, or a line per status change elsewhere. It finds the logs through the container's
`awslogs` options: `awslogs-group`, `awslogs-stream-prefix`, and `awslogs-region` when set. A container that logs
elsewhere, or without a stream prefix, runs without streaming, and migrate says so. CloudWatch can lag behind a failing
migration's last lines, so migrate reads them once more a poll after the task stops.

`--dry-run` describes the task definition and prints the image change and the planned run, then stops. It registers and
runs nothing.

`--launch-type` defaults to `FARGATE`. Set it to `EC2` when container instances back the task definition rather than
Fargate. The value must match the task definition's `requiresCompatibilities`, or ECS rejects the run with
`InvalidParameterException: Task definition does not support launch_type ...`.

When `--wait-timeout` passes, or three status checks in a row fail, migrate asks ECS once more. A task that has stopped
still reports its result; otherwise the command fails with the task's status, or why ECS can't tell. Ctrl-C ends the
wait as well. Migrate never stops the task, since that could interrupt a migration. It names the task instead: a task in
any state but `STOPPED` may yet change the database, so check it before retrying. With `--capacity-provider`, the
timeout includes any wait for the cluster to scale out.

`--capacity-provider` runs the task on a named capacity provider instead of a launch type. On a full EC2-backed cluster,
a launch type fails the run at once with `RESOURCE:ENI` or `RESOURCE:MEMORY`. A capacity provider with managed scaling
instead holds the task in `PROVISIONING` and adds an instance for it. Set one or the other, not both.

**NOTE**: Migrate resolves AWS credentials the standard way: `AWS_PROFILE`, an SSO login, or a CI role. That identity
needs permission to describe and register task definitions, run and describe tasks on the cluster, pass the task's roles
(`iam:PassRole`), and read the container's log group (`logs:GetLogEvents`).

## Why psql as the default?

The `psql` CLI comes with an installation of postgresql by default, so it makes it very likely to exist on any system
trying to use this command. It also is performant and well maintained.

## FAQ

**Q:** I'm having trouble accessing my rds DB, what should I do next? **A:** First confirm you have your AWS_PROFILE set
correctly, and you have done an `aws sso login` (or whatever form of login you do). Then double-check the config. When
in doubt, confirm you can log in via psql on your local terminal. As this is a wrapper, generally the issue lies in
config/login rather than the tool itself.
