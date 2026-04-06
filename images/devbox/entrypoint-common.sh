#!/bin/bash
# Common setup sourced by both terminal and agent entrypoints.
# Claude config symlink + backup restore.

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

# Start agent sidecar exec server if available
if [ -x /opt/agent-sidecar/claude-exec-server ]; then
    /opt/agent-sidecar/claude-exec-server &
    echo "[entrypoint] started agent sidecar exec server"
fi
