#!/bin/bash
set -e

# Common setup (Claude config symlink, backup restore)
source /usr/local/bin/entrypoint-common.sh

# --- Terminal setup ---

# DEVBOX_APP controls which application runs in the terminal.
export DEVBOX_APP="${DEVBOX_APP:-bash}"

# Codex: append approval mode flag.
if [ "$DEVBOX_APP" = "codex" ] && [ -n "$CODEX_APPROVAL_MODE" ] && [ "$CODEX_APPROVAL_MODE" != "suggest" ]; then
    export DEVBOX_APP="codex -a $CODEX_APPROVAL_MODE"
fi

# Claude Code: append skip-permissions flag.
if [ "$DEVBOX_APP" = "claude" ] && [ "$CLAUDE_SKIP_PERMISSIONS" = "true" ]; then
    export DEVBOX_APP="claude --dangerously-skip-permissions"
fi

# supervisord manages portpilot (auto-restart) and ttyd (critical).
# If ttyd dies, the event listener kills supervisord → container exits.
exec supervisord -c /etc/supervisord.conf
