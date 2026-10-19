#!/bin/sh
# Prepare the data volume and drop the privileges: the bot itself never needs
# root. It only writes the SQLite database, and the token it keeps in memory
# must not be reachable by a code execution bug in the container.
#
# The chown also fixes a volume that was created by an older (root) version
# of the image, so an update does not break an existing installation.
set -e

DATA_DIR="${DB_PATH%/*}"
if [ "$DATA_DIR" = "$DB_PATH" ]; then
  DATA_DIR="/data"
fi

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DATA_DIR" 2>/dev/null || true
  chown -R 10001:10001 "$DATA_DIR" 2>/dev/null || true
  exec su-exec 10001:10001 "$@"
fi

exec "$@"
