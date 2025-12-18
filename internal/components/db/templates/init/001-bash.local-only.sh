cat <<EOT >> ${PGDATA}/postgresql.conf
shared_preload_libraries='pg_cron,auto_explain,pg_stat_statements'
cron.database_name='${POSTGRES_DB:-postgres}'
EOT

pg_ctl restart