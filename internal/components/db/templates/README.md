# README

This directory is meant to house all the necessary files to manage the postgres database locally and on a deployed env.

## Structure vs. 200-Schema Files

The structure.sql file is meant to be the output of the 200-schema.sql and all migrations that exist in the main folder
(i.e. they haven't been moved off to the dated folder where they don't run). Whenever you add or move a migration you
should always run `mise update-structure-sql`. If you are moving migrations to the dated folder, you should also update
the 200-schema.sql file by running `mise update-init-schema`.

## Migrations

### Creating a Migration

You can create a migration by running `mise create-migration <your_migration_name>`, which will add the migration to the
[migrations](./migrations) folder. After you have done that, you should run `mise update-structure-sql` to update the
structure file, and then you can run `mise generate-sqlc` to ensure that all generated sql code is up to date.

### Migrations Tool

We use the migrate tool from [golang-migrate](https://github.com/golang-migrate/migrate) to handle migrations. They have
a docker, or you can use their go code directly to run your migrations as well. We choose to just use the docker as our
use cases don't have the complexity needed to run Go code directly.