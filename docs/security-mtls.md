# mTLS Security Model

## Current State

The kernel generates a CA on first boot and issues per-plugin certificates. Certs are bind-mounted into plugin containers via the Docker runtime. The plugin SDK loads them automatically.

Transport encryption (HTTPS between kernel and plugins) is working. However, **plugin authentication still relies on JWT bearer tokens** rather than certificate identity.

## How Plugins Get Certs

1. Kernel generates cert signed by CA (CN = plugin ID)
2. Writes to `data/kernel/certs/{plugin-id}/` on host filesystem
3. Bind-mounts into container at `/certs` (read-only)
4. Sets env vars: `TEAMAGENTICA_TLS_CERT`, `TEAMAGENTICA_TLS_KEY`, `TEAMAGENTICA_TLS_CA`

Certs never cross the network. Filesystem-only handoff.

## How Certs Are Validated

- Signed by our CA → valid (TLS handshake succeeds)
- Not signed by our CA → TLS handshake fails, connection rejected
- Expired → rejected
- CN checked at middleware level to identify the plugin

## Open Questions: Certificate Revocation

There is currently **no revocation mechanism**. If a cert is compromised, there is no way to invalidate it before it expires.

### Options Under Consideration

**Option A — Delete and regenerate (simplest)**
- Kernel deletes cert files, generates new ones, restarts container
- Old cert becomes unreachable (files gone from mount)
- Only works because we control the filesystem
- Does NOT protect against extracted certs used from outside Docker

**Option B — Short-lived certs + auto-rotation**
- Issue certs with short validity (e.g. 24 hours)
- Plugin SDK auto-refreshes via kernel endpoint
- Kernel stops issuing to block a plugin
- More complex, requires rotation logic in SDK

**Option C — CRL (Certificate Revocation List)**
- Kernel maintains a revocation list checked per request
- Standard TLS approach
- Adds per-request overhead

### Unresolved Attack Vectors

1. **Extracted cert from running container** — If an attacker gains shell access to a plugin container and exfiltrates the cert/key, they could impersonate that plugin from another host on the Docker network. Options B or C would mitigate this.

2. **Cert reuse after container removal** — If a container is stopped but its cert files remain on disk, those certs are technically valid until expiry. Currently certs are issued with 1-year validity.

3. **No per-request identity verification** — The current middleware (`PluginTokenAuth`) verifies JWT tokens but does not extract identity from the client certificate. The Phase 3 work adds a `MTLSPluginAuth` middleware that extracts plugin ID from the cert CN.

## Decisions Needed

- Which revocation strategy to implement (A, B, C, or combination)?
- Should cert validity be shortened from 1 year?
- Should we add network-level restrictions (e.g. only accept plugin connections from the Docker network)?
- How should third-party plugin providers handle certs when they don't control the host filesystem?
