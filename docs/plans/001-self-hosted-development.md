# Self-Hosted Development: System Editing Itself

> The system uses itself to develop itself. Production runs 24/7, development is a temporary candidate overlay with promote/rollback.

## Philosophy: Production-First Development

The system runs 24/7 as production. Development is a temporary overlay — deploy a candidate for the plugin you're working on, test against the live system, promote or rollback.

- **Production is the baseline** — all plugins run as stable built images
- **Development targets one plugin at a time** — only the plugin under development is unstable
- **Everything else stays production** — real data, real integrations, real behavior
- **Resilient to failure** — if a candidate breaks, auto-rollback keeps the system alive. Critical when operating from inside the system (e.g., from a phone)

---

## Already Implemented

### Candidate Deployment (kernel)
- `POST /api/plugins/:id/deploy` — starts candidate alongside primary
- `POST /api/plugins/:id/promote` — atomic swap, saves previous image for rollback
- `POST /api/plugins/:id/rollback` — reverts (stops candidate or restores previous)
- Plugin model has full candidate fields: `CandidateContainerID`, `CandidateHost`, `CandidatePort`, `CandidateHealthy`, `CandidateDeployedAt`, `PreviousImage`, `PreviousVersion`
- Candidate container: `teamagentica-{plugin-id}-candidate`, same network, `TEAMAGENTICA_CANDIDATE=true` env
- SDK candidate-aware registration and heartbeats

### Health Monitoring (kernel)
- `checkCandidates()` runs every 30s, marks unhealthy if heartbeat stale >90s
- Emits event on candidate health degradation

### tacli CLI
- `tacli plugin candidate <id> [--image IMAGE]` — deploy candidate
- `tacli plugin promote <id>` — promote to primary
- `tacli plugin rollback <id>` — revert

### Devbox Image
- Go 1.24, Node 22, git, Claude Code CLI
- Base for all workspace containers

---

## To Build: Kaniko + Local Registry + Auto-Rollback

### Why not Docker socket?
Building images traditionally requires Docker socket access — full Docker API control, effectively root. Instead: Kaniko builds OCI images in userspace (no daemon, no privileges) and pushes to a local registry. No container needs Docker socket.

### 1. Local Registry Plugin (`infra-docker-registry`)

Stock `registry:2` image, no custom code. Just a `plugin.yaml`.

- Image: `registry:2`
- Port: 5000
- Capability: `infra:docker-registry`
- All containers push/pull via `infra-docker-registry:5000/<image>:<tag>`
- Data persists to volume

### 2. Kaniko in Devbox Image

Install kaniko executor binary in devbox. Builds OCI images from source without a daemon.

```bash
/usr/local/bin/kaniko \
  --context /workspace/plugins/<name> \
  --destination infra-docker-registry:5000/teamagentica-<name>:candidate \
  --insecure
```

### 3. tacli in Devbox Image

Build from source in a multi-stage Dockerfile step (`CGO_ENABLED=0`, static binary). Set `TACLI_KERNEL=http://teamagentica-kernel:8080` as workspace env default.

### 4. Auto-Rollback on Candidate Health Failure

`checkCandidates()` in the health monitor currently marks unhealthy but takes no action. Add: if candidate unhealthy after grace period, stop candidate, clear candidate fields, emit rollback event. Primary untouched.

---

## Self-Hosted Dev Loop

```
1. Create workspace with git_repo = teamagentica repo
2. tacli connect $TACLI_KERNEL --email <email> --password <pass>
3. Edit plugin code in /workspace/plugins/<name>/
4. kaniko --context . --destination infra-docker-registry:5000/teamagentica-<name>:candidate --insecure
5. tacli plugin candidate <name> --image infra-docker-registry:5000/teamagentica-<name>:candidate
6. Candidate starts alongside primary, healthcheck runs
7a. Healthy → tacli plugin promote <name>
7b. Unhealthy → auto-rollback, primary untouched
```

---

## Safety

- **Both containers alive** — candidate runs alongside primary, no downtime gap
- **Auto-rollback** — unhealthy candidate auto-removed, primary keeps running
- **Explicit promote** — user controls when candidate becomes primary
- **No privileged access** — kaniko builds without Docker socket
- **Build ≠ deploy** — building an image doesn't deploy it
- **Network resilience** — if you're operating from inside the system, a bad candidate can't take down the network

---

## Future Improvements (not in scope)

- Candidate data isolation (cloned data volume per candidate)
- Test traffic routing to candidate before promotion
- Auto-promote after N seconds of healthy candidate
- Multi-version rollback history (currently one-deep)
