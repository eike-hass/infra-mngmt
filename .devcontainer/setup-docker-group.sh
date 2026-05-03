#!/bin/bash
# Align the in-container `docker` group's GID to the GID of the bind-mounted
# host docker socket, so the `node` user can talk to the daemon without sudo.
# Run at postStart, after the socket mount is visible.
set -euo pipefail

SOCK=/var/run/docker.sock
if [ ! -S "$SOCK" ]; then
  echo "setup-docker-group: no socket at $SOCK; docker CLI will not work"
  exit 0
fi

SOCK_GID=$(stat -c '%g' "$SOCK")
CURRENT_GID=$(getent group docker | cut -d: -f3 || true)

if [ "$CURRENT_GID" = "$SOCK_GID" ]; then
  echo "setup-docker-group: docker group already aligned (gid $SOCK_GID)"
  exit 0
fi

EXISTING=$(getent group "$SOCK_GID" | cut -d: -f1 || true)
if [ -z "$EXISTING" ]; then
  groupmod -g "$SOCK_GID" docker
  echo "setup-docker-group: docker gid $CURRENT_GID -> $SOCK_GID"
elif [ "$EXISTING" = "docker" ]; then
  echo "setup-docker-group: docker group already has gid $SOCK_GID"
else
  usermod -aG "$EXISTING" node
  echo "setup-docker-group: node added to existing group $EXISTING (gid $SOCK_GID)"
fi
