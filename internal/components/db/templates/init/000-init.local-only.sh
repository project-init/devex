#!/bin/sh
# Install pg_cron package

set -e

if [ "$(id -u)" = '0' ]; then
  echo "installing pg_cron"
  apt-get update
  apt-get install -y postgresql-17-cron
  apt-get clean
fi

docker-entrypoint.sh "$@"
