Perform a security review of the TeamAgentica system, find vulnerabilities, create a task board, and fix them.

Arguments: $ARGUMENTS

The arguments can be:
- A focus area (e.g., "auth", "storage", "webhooks", "plugins", "injection")
- "full" for a comprehensive review of all areas
- A board name to resume working on an existing security review board

## Setup

Use the **task-flow** skill conventions for board API setup and auth.

Security tests live in: `security-testing/`
Run tests with: `cd security-testing && ADMIN_EMAIL=test@teamagentica.localhost ADMIN_PASSWORD='5c:m7g6TByfMKH8' npx vitest run <test-file>`

## Phase 1: Reconnaissance

Launch parallel Explore agents to analyze the attack surface. Focus areas:

### Auth & Identity
- JWT implementation (signing, validation, expiry, secret management)
- Session cookies (flags, scope, timeout)
- User context headers (X-User-ID, X-User-Email, X-User-Role) — are they signed? Can plugins spoof them?
- Service tokens (creation, scoping, revocation)
- Rate limiting on auth endpoints

### API Routing & Access Control
- Plugin proxy routes (`/api/route/:plugin_id/*`) — what auth is enforced?
- Webhook ingress (`/api/webhook/:plugin_id/*`) — no auth by design, but what validation exists?
- Workspace container proxy (`/ws/:container_id/*`) — security-by-obscurity via container ID?
- CORS policy — origin validation, wildcard misuse
- CSP headers — frame-ancestors, caller-controlled values

### Inter-Plugin Security
- Event subscription system — can any plugin subscribe to any event?
- Plugin-to-plugin routing — ACLs? Namespace isolation?
- Webhook route registration via events — can a plugin hijack another's webhooks?
- mTLS between kernel and plugins — certificate validation depth

### Storage & Data
- storage-volume: path traversal protection, per-user isolation
- storage-sss3: S3 key validation, access control
- Plugin config secrets (`is_secret` flag) — are they masked in all API responses?
- Database access patterns — row-level security? Multi-tenancy?

### Input Validation
- SQL injection vectors (plugin IDs, config values, search queries)
- XSS vectors (plugin names, user display names, config values)
- Path traversal (routing paths, log endpoints, file keys)
- Command injection (config values, container image names)
- SSRF (marketplace URLs, plugin registration URLs)
- Request size limits, type confusion, HTTP method tampering

## Phase 2: Create Security Review Board

Create a new board on the task tracker:
```
POST /boards
{ "name": "Security Review: <focus-or-date>", "description": "Automated security audit findings" }
```

Create columns following the task-flow board column convention.

## Phase 3: Report Findings as Cards

For each vulnerability found, create a card in the **Todo** column:

```
POST /boards/:id/cards
{
  "column_id": "<todo-column-id>",
  "title": "<short vulnerability title>",
  "description": "<detailed description>",
  "priority": "<severity>",
  "labels": "<category>"
}
```

**Priority mapping:**
- CRITICAL → "urgent"
- HIGH → "high"
- MEDIUM → "medium"
- LOW → "low"

**Labels** (comma-separated): auth, injection, access-control, storage, webhooks, crypto, config, disclosure, dos, cors, headers

**Description format:**
```
## Vulnerability
<What the issue is>

## Impact
<What an attacker could do>

## Location
<File paths and line numbers>

## Reproduction
<Steps or test to demonstrate the issue>

## Suggested Fix
<How to remediate>
```

Add a comment to each card with the initial finding details and any proof-of-concept test results.

## Phase 4: Fix Vulnerabilities

For **each** card, starting with highest priority, use the **bug-fix** skill to fix it.

When invoking bug-fix for each card, ensure the agent context includes these security-specific instructions:
- Security tests live in `security-testing/src/` — if a test exists for this category, run it
- If no test exists, write one in the appropriate `security-testing/src/` subdirectory following existing patterns
- Test command: `cd security-testing && ADMIN_EMAIL=test@teamagentica.localhost ADMIN_PASSWORD='5c:m7g6TByfMKH8' npx vitest run <test-file>`
- Never weaken existing security measures to fix a different issue

After the bug-fix skill marks a card as "Done", do a **double-verify** from the main context:
- Run the security test(s) again yourself
- If verification passes → card stays "Done"
- If verification fails → move back to "In Progress" and re-invoke bug-fix with the failure context

After all cards processed, print summary:

```
| # | Vulnerability | Severity | Status | Attempts | Notes |
|---|---------------|----------|--------|----------|-------|
| 1 | Unsigned user headers | HIGH | Done | 2 | Added HMAC validation |
| 2 | No storage ACLs | MEDIUM | Failed | 10 | Needs architectural change |
```

## Important Rules (security-specific)

The bug-fix and task-flow skills handle general rules (comments, testing, builds, deploys, minimal fixes). These are additional security-specific rules:

- For SDK changes: update go.mod in all affected plugins, rebuild them
- When writing new tests, follow the existing patterns in security-testing/src/
- Never weaken existing security measures to fix a different issue
- Document everything — future reviewers need to understand what was found and how it was fixed
