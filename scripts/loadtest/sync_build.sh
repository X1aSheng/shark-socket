#!/bin/bash
# Copy the working tree to a remote host and build it there.
#
# Load testing needs the same source on both machines, and the generator has to
# match the server: a mismatch produces numbers that are not about the server.
# The default path is a git fetch rather than a transfer of this checkout, so the
# remote builds the committed tree rather than whatever happens to be in the
# working directory.
#
# Usage:
#   REMOTE_HOST=10.0.0.1 REMOTE_USER=root bash sync_build.sh
#
# Environment:
#   REMOTE_HOST   target (required)
#   REMOTE_USER   ssh user          (default root)
#   REMOTE_REPO   checkout path     (default /root/shark-socket)
#   REF           git ref to build  (default HEAD)
set -uo pipefail

REMOTE_HOST="${REMOTE_HOST:?set REMOTE_HOST, e.g. REMOTE_HOST=10.0.0.1}"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_REPO="${REMOTE_REPO:-/root/shark-socket}"
REF="${REF:-HEAD}"

SRC_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "not inside a git repository" >&2
  exit 1
}
SHA=$(git rev-parse --short "$REF" 2>/dev/null || echo unknown)
ORIGIN=$(git config --get remote.origin.url)

echo "SYNC ref=$REF sha=$SHA -> $REMOTE_USER@$REMOTE_HOST:$REMOTE_REPO"

# Fetch into FETCH_HEAD rather than checking out a branch: a branch checkout on
# the remote would fail on a dirty tree there, and that failure reads as a sync
# problem rather than as an uncommitted local edit.
#
# accept-new on the host key rather than strict: these are throwaway benchmark
# hosts, and a first connection failing on host key verification aborts the
# whole run before any load is generated. It still refuses a CHANGED key, which
# is the case that actually indicates interception.
ssh -o StrictHostKeyChecking=accept-new "$REMOTE_USER@$REMOTE_HOST" "set -e
  if [ ! -d '$REMOTE_REPO/.git' ]; then
    git clone --quiet '$ORIGIN' '$REMOTE_REPO'
  fi
  cd '$REMOTE_REPO'
  git fetch --quiet origin $REF
  git checkout --quiet --force FETCH_HEAD
  echo \"remote sha=\$(git rev-parse --short HEAD)\"
  export PATH=/usr/local/go/bin:\$PATH
  go build -o /root/protocolgen ./scripts/protocolgen
  go build -o /root/shark-multi ./cmd/shark-socket
  echo BUILD_OK
"

echo "SYNC done; local sha=$SHA"
echo "verify the two shas match before trusting any number from that host"