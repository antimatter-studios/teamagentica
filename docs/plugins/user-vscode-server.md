# user-vscode-server

> Provides VS Code (code-server) as a browser-based workspace environment.

## Overview
Lightweight plugin that declares a VS Code workspace environment. The workspace-manager uses this plugin's schema to know how to create VS Code containers.

## Capabilities
- `workspace:environment`

## Config
| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | false | Debug logging |

## Workspace Schema
```json
{
  "display_name": "VS Code",
  "description": "Full IDE with terminal, extensions, and git support",
  "image": "codercom/code-server:latest",
  "port": 8080,
  "docker_user": "coder",
  "cmd": ["--auth", "none", "--bind-addr", "0.0.0.0:8080", "--extensions-dir", "/workspace/.code-server/extensions", "--disable-telemetry", "/workspace"],
  "env_defaults": {
    "DEFAULT_WORKSPACE": "/workspace"
  }
}
```

## Extension Persistence
VS Code extensions are stored in `/workspace/.code-server/extensions/` within the workspace volume. Extensions survive container restarts and are detected by the workspace-manager's tag system.

## Adding New Environments
To add a new workspace environment (e.g., JupyterLab), create a new plugin with `workspace:environment` capability and declare the workspace schema. The workspace-manager will automatically discover it.
