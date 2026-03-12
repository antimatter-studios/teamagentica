#!/bin/bash
set -e

# --- Runtime setup ---

# Claude Code stores config at ~/.claude.json but data at ~/.claude/.
# Symlink so the config file lives inside the shared volume.
if [ ! -L "$HOME/.claude.json" ] && [ ! -f "$HOME/.claude.json" ]; then
    ln -sf "$HOME/.claude/.claude.json" "$HOME/.claude.json"
fi

# Auto-restore Claude config from backup if missing.
if [ ! -f "$HOME/.claude/.claude.json" ]; then
    backup=$(ls -t "$HOME/.claude/backups/.claude.json.backup."* 2>/dev/null | head -1)
    if [ -n "$backup" ]; then
        cp "$backup" "$HOME/.claude/.claude.json"
        echo "Restored Claude config from backup: $backup"
    fi
fi

# --- Start services ---

# DEVBOX_APP controls which application runs in the terminal.
# Plugin config env vars append CLI flags to the base command.
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
