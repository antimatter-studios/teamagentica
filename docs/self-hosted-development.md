# Self-Hosted Development: System Editing Itself

> Design doc — the system uses itself to develop itself. Production runs 24/7, development is a temporary candidate overlay with promote/rollback.

## Philosophy: Production-First Development

The system runs 24/7 as production. Development is a temporary overlay — deploy a dev candidate for the plugin you're working on, test against the live system, promote or rollback.

- **Production is the baseline** — all plugins run as stable built images
- **Development targets one plugin at a time** — only the plugin under development is unstable
- **Everything else stays production** — real data, real integrations, real behavior
- **docker-compose becomes bootstrap-only** — used once to stand up the initial system, then the system manages itself

### Two development strategies within this model

**Dev candidate (fast iteration)**
- Deploy a dev container with Air + source mounts as candidate
- Edit in workspace → Air rebuilds → test against live system
- No Docker build needed — just file routing + hot-reload

**Prod candidate (release cycle)**
- Build a new image via infra-builder from workspace source
- Deploy as candidate alongside current prod
- Verify, then promote to become the new primary

---

## Dev-Mode Workflow

### How it works today
- Plugin container bind-mounts `projectRoot/plugins/{name}` → `/app/plugins/{name}`
- Air watches at 1s poll interval, rebuilds Go binary on file change
- Plugin dir derived from image: `teamagentica-messaging-discord:dev` → `messaging-discord`

### What's needed: workspace that edits the same source
- Docker bind mounts are NOT exclusive — workspace + plugin mount same host dir
- Edits propagate instantly, Air detects → rebuilds → plugin restarts

### Implementation
1. New `plugin_source` field on CreateWorkspace
2. `StartManagedContainer` — if PluginSource set, bind-mount plugin source dir + SDK + Go caches
3. Workspace-manager passes `plugin_source` through to kernel

### Files to change (dev mode)
- `kernel/internal/models/managed_container.go` — add `PluginSource string`
- `kernel/internal/runtime/docker.go:StartManagedContainer` — mount source dir + SDK + Go caches (~15 lines)
- `plugins/infra-workspace-manager/internal/handlers/workspaces.go` — pass plugin_source
- `pkg/pluginsdk/sdk.go` — add PluginSource to CreateManagedContainerRequest

---

## Traffic Splitting & Candidate Routing (kernel-level)

### Concept: both containers stay alive
- **Primary**: the production plugin container (stable image)
- **Candidate**: a dev or new-build container running alongside
- Kernel proxy (`RouteToPlugin`) routes to candidate if healthy, falls back to primary
- Both containers registered under the same plugin ID but at different host:port

### Plugin model changes
```
CandidateContainerID  string
CandidateHost         string
CandidatePort         int
CandidateHealthy      bool
CandidateDeployedAt   time.Time
PreviousImage         string
PreviousVersion       string
```

### RouteToPlugin() change (~30 lines)
Current: DB lookup → proxy to `plugin.Host:plugin.HTTPPort`
New: if `CandidateHost != ""` AND `CandidateHealthy` → proxy to candidate; else → proxy to primary

### Health monitor change (~20 lines)
- Check candidate container health independently (same mechanism as primary)
- If candidate unhealthy for >90s: set `CandidateHealthy=false`, traffic auto-falls back to primary
- Emit `candidate:unhealthy` event so UI/agents know

### Deploy flow
1. `POST /api/plugins/:id/deploy` with `{"image": "..."}` or `{"dev_mode": true}`
2. Kernel starts candidate container (different name, same network)
3. Sets `CandidateHost/Port/ContainerID` on plugin model
4. Health monitor starts checking candidate
5. Once candidate registers + healthy → `CandidateHealthy=true` → traffic routes there
6. Primary stays alive as fallback

### Dev-mode deploy variant
1. `POST /api/plugins/:id/deploy` with `{"dev_mode": true}`
2. Kernel starts a dev container (Air, source mounts) as candidate
3. Developer edits in workspace → Air rebuilds → candidate restarts
4. Traffic goes to candidate (dev version) while primary stays untouched
5. If candidate crashes, traffic falls back to primary within seconds

### Data safety: pre-deploy DB snapshots
- Before starting candidate, kernel snapshots the candidate plugin's own DB files
- SQLite: copy `.db`, `.db-wal`, `.db-shm` as a set → `{name}.pre-candidate`
- On rollback: stop candidate, restore DB snapshot, resume primary
- On promote: delete snapshot (candidate's writes are now canonical)
- Snapshot logic lives in deploy handler (~10 lines) — generic file copy, not plugin-specific
- Only snapshots the candidate's own data volume — not other plugins

### Cross-service data consistency
- Candidate may call other plugins (e.g. create volumes, send messages) — those changes persist even on rollback
- This is normal microservice behavior — no distributed rollback across services
- Plugins MUST handle missing cross-references gracefully (404, not crash)
- Orphaned data in other services is harmless clutter, cleaned lazily
- System-wide snapshot (all plugins) is too heavy and not worth the complexity at this stage

### Promote / Rollback
- `POST /api/plugins/:id/promote` — stop primary, candidate becomes new primary, clear candidate fields, delete DB snapshot
- `POST /api/plugins/:id/rollback` — stop candidate, restore DB snapshot, clear candidate fields, primary resumes
- Auto-rollback: if candidate unhealthy >90s, same as manual rollback (includes DB restore)

### Plugin-type-specific failover behavior
- **Stateless plugins** (messaging, AI chat): seamless — next request goes to primary
- **Workspace-manager**: state is in DB/volumes, not in-memory — failover is clean
- **Storage-volume**: file operations are idempotent — failover is safe
- **WebSocket plugins** (Discord bot): active connections drop on failover, client reconnects

### Candidate registration
- Kernel sets `TEAMAGENTICA_CANDIDATE=true` env var on candidate container
- SDK reads env var, includes `"candidate": true` in register/heartbeat requests
- `SelfRegister()`: if `candidate=true` → updates CandidateHost/Port/Healthy (not primary fields)
- Heartbeats: candidate heartbeats keep `CandidateHealthy` fresh; stale → `CandidateHealthy=false`
- Container name: `teamagentica-{plugin-id}-candidate` (different from primary)
- Same plugin ID, same token, same network, different hostname

### Kernel files to change
- `models/plugin.go` — add Candidate* and Previous* fields
- `handlers/plugin_registration.go:SelfRegister()` — candidate-aware registration (~15 lines)
- `handlers/plugin_registration.go:Heartbeat()` — candidate-aware heartbeat (~10 lines)
- `handlers/plugin_registration.go:RouteToPlugin()` — candidate routing (~30 lines)
- `handlers/plugins.go` — deploy/promote/rollback handlers (~120 lines)
- `runtime/docker.go` — `StartCandidateContainer()` (~40 lines, reuses StartPlugin with candidate env)
- `health/monitor.go` — candidate health checking (~20 lines)
- `pkg/pluginsdk/sdk.go` — read TEAMAGENTICA_CANDIDATE env, include in register/heartbeat (~5 lines)

### Future: extract to infra-traffic plugin
If routing needs grow (canary %, A/B testing, per-user routing), extract the candidate routing logic from the kernel into a dedicated `infra-traffic` plugin or Envoy sidecar. The kernel proxy becomes a simple pass-through.

---

## SDK Modularization (nested Go module)

### Current state
- `pkg/pluginsdk/` has its own `go.mod` but all plugins use `replace` directives:
  ```
  replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
  ```
- Dockerfiles must `COPY pkg/pluginsdk/` from repo root → forces full-repo build context

### Target state
- `pkg/pluginsdk/` is a proper Go module, tagged independently (e.g. `pkg/pluginsdk/v1.0.0`)
- Plugins import it by version: `require .../pkg/pluginsdk v1.0.0`
- No `replace` directives in committed code (dev can use them locally via `go.mod` edits)
- Plugins are self-contained — `go mod download` fetches SDK from git

### How Go nested module tagging works
- Tag format: `pkg/pluginsdk/v1.0.0` (path prefix + semver)
- Go toolchain recognizes this as the version for that module path
- `go get github.com/antimatter-studios/teamagentica/pkg/pluginsdk@v1.0.0`
- Works with any git host (GitHub, self-hosted) — no registry needed

### Impact on Dockerfiles
Before (needs full repo):
```dockerfile
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/messaging-discord/ app/
```

After (plugin-only context):
```dockerfile
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o server .
```

### Impact on builder
- Build context = just the plugin directory (not full repo)
- Plugin-only workspaces work for BOTH editing and building
- No need for full-repo volumes — simpler, faster builds

### Dev-mode workflow with local SDK changes
- Developer editing both SDK and plugin simultaneously:
  1. Workspace mounts plugin source (Air hot-reload)
  2. Add temporary `replace ../../../pkg/pluginsdk` in plugin's go.mod
  3. Edit SDK → Air rebuilds plugin → instant feedback
  4. When done: remove `replace`, tag new SDK version, update plugin's go.mod
- Or: just edit plugin code against the published SDK version (most common case)

---

## infra-builder Plugin (capability: `build:docker`)

### Purpose
Builds Docker images from source in storage-volume volumes. Only plugin with `docker.sock`.

### API
- `POST /build` — build image from volume source + Dockerfile (NDJSON streaming response)
  - Request: `{"volume": "my-repo", "dockerfile": "plugins/my-plugin/Dockerfile", "image": "teamagentica-my-plugin", "tag": ""}`
  - `volume` (required): storage-volume name containing full repo source
  - `dockerfile` (required): path relative to volume root
  - `image` (required): image name (no tag)
  - `tag` (optional): defaults to `{yyyymmdd-HHmmss}` timestamp
  - Response: NDJSON stream — `{"stream":"..."}` lines + final `{"result":{...}}`
- `GET /builds` — recent build history (in-memory ring buffer, 50 entries)
- `GET /builds/:id/logs` — stored build logs

### Build context strategy
- With SDK modularized: build context = plugin directory only
- Build context = `/workspaces/volumes/{volume}` (the plugin source)
- Dockerfile at root of plugin dir (e.g., `Dockerfile` within the volume)
- `go mod download` fetches SDK + dependencies — no monorepo needed
- Same workspace used for editing AND building

### Build mechanics
- Docker client `ImageBuild()` with tar context via `archive/tar`
- Always `--target prod` (3-stage Dockerfiles: dev → builder → prod)
- Tag convention: `teamagentica-{plugin}:{yyyymmdd-HHmmss}`
- `.dockerignore` respected automatically by Docker API
- `Remove: true` + `ForceRemove: true` cleans intermediate containers
- Concurrent builds serialized via `sync.Mutex` — second request gets 409

### Build history
- In-memory `[]BuildRecord` ring buffer, 50 entries
- Fields: id, image, volume, dockerfile, status, started_at, duration_ms, logs
- No persistence — builder restart clears history (images are the durable artifact)

### Kernel change for docker.sock
```go
if hasCapabilityPrefix(plugin.GetCapabilities(), "build:docker") {
    mounts = append(mounts, mount.Mount{
        Type:   mount.TypeBind,
        Source: "/var/run/docker.sock",
        Target: "/var/run/docker.sock",
    })
}
```

### ~300 lines across 4 files
- `main.go` (~90 lines) — registration, router, config
- `internal/handlers/handlers.go` (~40 lines) — health, tools, build history struct
- `internal/handlers/build.go` (~170 lines) — build logic, tar context, streaming

---

## Workspace-Manager Orchestration Tools

- `POST /tool/build_plugin` — calls infra-builder's `/build` endpoint
- `POST /tool/deploy_plugin` — calls kernel `POST /api/plugins/:id/deploy`
- Dev mode: `plugin_source` option on CreateWorkspace

---

## Safety Summary
- **Both containers alive**: candidate runs alongside primary, no downtime gap
- **Health-based failover**: candidate unhealthy >90s → traffic auto-returns to primary
- **Explicit promote/rollback**: user controls when candidate becomes the new primary
- **DB snapshots**: candidate's own DB snapshotted before deploy, restored on rollback
- **Cross-service resilience**: plugins handle missing cross-references gracefully
- **Builder isolation**: only builder gets docker.sock
- **Two-step**: build ≠ deploy (no auto-deploy)
- **Dev mode**: Air crash → candidate unhealthy → fallback to prod → developer fixes → Air rebuilds → traffic returns

## Bootstrap: From Zero to Production-First

### Initial setup (one-time)
1. `docker-compose up` — starts kernel + UI + all plugins from pre-built images
2. All plugins register as primary containers — system is now "production"
3. docker-compose is never used for development again

### Daily development workflow
1. Open workspace for the plugin you want to modify
2. Deploy dev candidate: `POST /api/plugins/:id/deploy {"dev_mode": true}`
3. Traffic routes to your dev candidate — test with real system
4. Edit code in workspace → Air rebuilds → candidate restarts
5. Happy? Promote candidate to primary. Problem? Rollback to previous primary.
6. Build new prod image from workspace source via infra-builder
7. Deploy prod candidate from new image → verify → promote

### What this replaces
- No more `docker-compose up` for daily development
- No more "everything runs in dev mode" — only your target plugin is dev
- No more synthetic test environments — test against real production data
- No more "works in dev, breaks in prod" — dev IS prod (with safety net)

---

## Not building yet
- Multi-version rollback history (one-deep sufficient)
- Envoy / external traffic shaper (kernel proxy for now, extract later if needed)
- Per-user routing or canary %

---

## Future Stage: Registry-Based, No-Local-Source Model

### Vision
No source code on your machine. GitHub is the single source of truth. System self-assembles from container registry.

### How it works
1. **Bootstrap** — single script (or `docker run`) starts kernel + UI. No git clone needed.
2. **Kernel pulls plugin images from ghcr.io** — plugin catalog specifies registry paths, kernel pulls and starts them.
3. **CI/CD** — GitHub Actions builds prod images on merge to main, pushes to ghcr.io. Tagged images = production.
4. **Development** — git clone a branch into a workspace (inside the system), edit, build candidate, test, push, open PR.
5. **Merge = deploy** — PR merged → CI builds new image → system pulls updated image → new primary.

### What changes from current plan
- Kernel container management adds registry auth + `docker pull` (Docker handles this natively)
- Plugin catalog stores `ghcr.io/antimatter-studios/teamagentica-{plugin}:latest` as image source
- Bootstrap script replaces docker-compose for initial setup
- Workspaces do `git clone` instead of mounting local source

### Why this is stage 2, not stage 1
- The candidate routing, deploy/promote/rollback, and builder mechanics are identical regardless of image source
- Stage 1 builds all the machinery; stage 2 changes where images come from
- Can develop and test the full workflow locally first, then add registry support

---

## Implementation Order
1. **SDK modularization** — remove `replace` directives, tag `pkg/pluginsdk/v1.0.0`, update plugin go.mod files, simplify Dockerfiles (~10 files, mostly config)
2. **Dev-mode source mounting** — PluginSource field + StartManagedContainer (~4 files, ~30 lines)
3. **Kernel: candidate model fields** + RouteToPlugin routing (~2 files, ~50 lines)
4. **Kernel: deploy/promote/rollback** handlers + StartCandidateContainer (~3 files, ~160 lines)
5. **Kernel: candidate health monitoring** (~1 file, ~20 lines)
6. **Kernel: docker.sock mount** for `build:docker` capability (~8 lines)
7. **infra-builder plugin** (~300 lines)
8. **SDK helpers** — DeployPlugin/BuildPlugin (~30 lines)
9. **Workspace-manager tools** — build/deploy orchestration (~60 lines)
