#!/bin/sh
# Runs on the first boot of an empty Postgres volume (docker-entrypoint-initdb.d).
# The SQL lives outside initdb.d so the entrypoint does not also run it on
# its own, without the password variable.
set -eu
: "${TOPPER_DB_PASSWORD:?TOPPER_DB_PASSWORD must be set in the postgres service environment}"
psql -v ON_ERROR_STOP=1 -v topper_password="$TOPPER_DB_PASSWORD" \
    --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -f /topper-init/topper.sql
