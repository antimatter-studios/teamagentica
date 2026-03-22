Build and test-compile plugin(s) without deploying.

The user provides plugin name(s) as arguments: $ARGUMENTS

## Workflow

For **each** plugin name provided:

### 1. Build the Go binary

```bash
cd plugins/<name> && go build -o build/<name> ./...
```

The `build/` directory is gitignored (`**/build/`), so the binary won't pollute the working tree.

### 2. Report result

- If the build succeeds: report success
- If the build fails: report the error output

## Output

After all plugins are built, print a summary:

```
| Plugin        | Status  | Notes         |
|---------------|---------|---------------|
| agent-claude  | ok      |               |
| storage-sss3  | FAILED  | undefined: X  |
```

## Important rules

- Always output to `build/` — never leave binaries in the plugin root
- Build each plugin separately for visibility
- Do NOT deploy, submit, or restart — this is compile-check only
