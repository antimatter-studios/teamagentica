Build and restart the kernel.

## Workflow

### 1. Build the kernel

```bash
chore kernel:build
```

**Stop if the build fails.** Report the error and do not continue.

### 2. Restart the kernel

```bash
chore kernel:restart
```

### 3. Re-authenticate

```bash
chore kernel:connect
```

This refreshes the tacli auth token after the kernel restarts.

## Output

Report whether the build and restart succeeded or failed.
