#!/bin/bash
# Common setup sourced by both terminal and agent entrypoints.
# Claude config symlink + backup restore.

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

# Start agent sidecar exec server(s) if available.
# Each agent plugin ships its own *-exec-server binary (claude-exec-server,
# codex-exec-server, etc.) via the agent's sidecar shared disk.
for bin in /opt/agent-sidecar/*-exec-server; do
    if [ -x "$bin" ]; then
        "$bin" &
        echo "[entrypoint] started agent sidecar: $(basename "$bin")"
    fi
done
