#!/bin/bash
set -e

export EXTENSIONS_DIR="${EXTENSIONS_DIR:-/mnt/shared-extensions}"
export DEFAULT_WORKSPACE="${DEFAULT_WORKSPACE:-/workspace}"

exec supervisord -c /etc/supervisord.conf
