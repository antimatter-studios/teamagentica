Build, submit, and restart plugin(s) in one flow.

The user provides plugin name(s) as arguments: $ARGUMENTS

## Workflow

For **each** plugin name provided:

### 1. Read current version

```bash
grep '^version:' plugins/<name>/plugin.yaml
```

### 2. Bump version

Bump the patch component by 1 (e.g. `1.0.7` → `1.0.8`). Do not ask the user.

### 3. Build the Docker image

```bash
task plugin:build -- <name> --version <new-version>
```

This updates `plugin.yaml` with the new version and builds the production Docker image.

**Stop if the build fails.** Report the error and do not continue.

### 4. Submit manifest to marketplace

```bash
task marketplace:submit -- plugins/<name>/plugin.yaml
```

### 5. Upgrade the plugin

```bash
go run ./tacli marketplace upgrade-plugin <name>
```

This pulls the updated manifest metadata from the marketplace.

### 6. Restart the plugin

```bash
go run ./tacli plugin restart <name>
```

This restarts the plugin container with the new image.

## Output

After completion, print a summary table like:

```
| Plugin        | Before | After | Status    |
|---------------|--------|-------|-----------|
| agent-claude  | 1.0.7  | 1.0.8 | deployed  |
```

## Important rules

- Always auto-bump patch version — never ask the user for a version number.
- Never force-push Go module tags.
- If a build fails, stop and report — do not continue to submit or restart.
- Run builds sequentially (Docker builds are resource-intensive).
