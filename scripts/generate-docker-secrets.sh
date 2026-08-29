#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SECRETS_DIR="$REPO_ROOT/docker/secrets"
mkdir -p "$SECRETS_DIR"
umask 077

if [ ! -e "$SECRETS_DIR/master_key" ]; then
  openssl rand -base64 32 > "$SECRETS_DIR/master_key"
fi
if [ ! -e "$SECRETS_DIR/admin_token" ]; then
  openssl rand -base64 36 > "$SECRETS_DIR/admin_token"
fi
chmod 600 "$SECRETS_DIR/master_key" "$SECRETS_DIR/admin_token"
echo "Docker secrets created in docker/secrets. They are ignored by Git; back them up separately."
