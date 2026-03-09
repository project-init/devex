# Postgres

Collection of postgres tools meant to make it easier to manage your production clusters.

## Access

```shell
sre postgres access --access_file .sre
```

The access command uses an SRE yaml file (defaults to `.sre`) to determine what the login configuration is for your
given postgres clusters. Assumes you have done any SSO login or similar you might need to have access through your
vendor as well.
