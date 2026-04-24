#!/bin/bash
set -e

export EXTENSIONS_DIR="${EXTENSIONS_DIR:-/mnt/shared-extensions}"
export DEFAULT_WORKSPACE="${DEFAULT_WORKSPACE:-/workspace}"

# If GIT_REPO is set and /workspace is empty, clone on first start.
# Failure writes /workspace/.git-clone-error and lets the container start anyway
# so the user can open the workspace and fix credentials/URL manually.
if [ -n "${GIT_REPO:-}" ] && [ -z "$(ls -A /workspace 2>/dev/null)" ]; then
    if git clone "$GIT_REPO" /workspace 2>/workspace/.git-clone-error; then
        rm -f /workspace/.git-clone-error
        if [ -n "${GIT_REF:-}" ]; then
            (cd /workspace && git checkout "$GIT_REF") 2>>/workspace/.git-clone-error || true
            [ -s /workspace/.git-clone-error ] || rm -f /workspace/.git-clone-error
        fi
    fi
fi

exec supervisord -c /etc/supervisord.conf
