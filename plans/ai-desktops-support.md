# First-Class ai-desktops Support Plan

## Status

In progress. Phases 1 and 2 (minus runtime manifest packaging) and all documentation (§7) are complete and merged via PR #93. Phase 3 (ai-desktops-side provisioning and doctor check) is next.

## Context

`ai-agent-bridge` is already installed by the `ai-desktops` image on Ubuntu 24.04
and starts successfully as a systemd service. The packaged default is a useful
health-check baseline:

- `/usr/bin/ai-agent-bridge` is installed from the apt repository.
- `/etc/ai-agent-bridge/bridge.yaml` binds to `127.0.0.1:9445`.
- `/var/lib/ai-agent-bridge/sessions.db` persists session metadata and output.
- The daemon runs as the dedicated `ai-agent-bridge` system account.
- TLS and JWT are disabled for localhost-only desktop use.
- No providers are configured, so the service cannot start an AI agent session.

The current package is intentionally provider-neutral, but an `ai-desktops`
machine needs an opinionated agent-host profile. The profile must install pinned
provider CLIs, make workspace repositories available to agent subprocesses, and
provide credentials to the daemon without weakening the default package for
other users.

## Observed Gaps

### Provider Runtime

The desktop image currently has Node.js 18 and no `npm` executable. The bridge
repo pins Node.js 24 in `.nvmrc` and pins provider CLI versions in
`package.json`.

When providers are configured, daemon startup calls:

```go
config.ValidateNodeRuntime(".")
```

The packaged systemd service uses:

```ini
WorkingDirectory=/var/lib/ai-agent-bridge
```

That directory does not contain `.nvmrc`, so adding providers to the packaged
config causes daemon startup to fail before any provider can be registered.

### Workspace Access

The packaged config allows repository paths under `/home`, `/srv`, `/tmp`, and
`/var/tmp`. `ai-desktops` checks repositories out under `/workspace`, so session
requests for desktop repositories are denied by policy.

The systemd unit also uses:

```ini
ProtectSystem=strict
ReadWritePaths=/var/lib/ai-agent-bridge
```

Agent subprocesses inherit that sandbox and cannot modify `/workspace`, even if
the bridge policy allows the path.

### Credentials

Provider CLIs need API keys or provider-specific authentication. The apt package
does not define a credential injection mechanism for an agent-host deployment.
Secrets must not be written into the package, AMI, repository, or world-readable
configuration.

### Validation

Existing apt smoke tests prove that the provider-neutral daemon installs and
starts. They do not prove that an `ai-desktops` host can start a real provider
session against a repository under `/workspace`.

## Goals

- Preserve the minimal provider-neutral apt package as a valid default.
- Add a supported `ai-desktops` agent-host deployment profile.
- Install Node.js 24 and pinned AI agent CLIs in a stable system path.
- Allow bridge-managed agents to operate on repositories under `/workspace`.
- Inject credentials through a root-controlled runtime mechanism.
- Keep the bridge bound to localhost by default.
- Add automated validation for package startup and desktop agent-host startup.
- Document the operator workflow for provisioning, upgrading, and debugging.

## Non-Goals

- Expose the bridge publicly over the desktop hostname.
- Store API keys in the repository, apt package, AMI, or desktop metadata.
- Replace the existing mTLS and JWT support for non-local deployments.
- Couple the apt package to a specific cloud secrets backend.
- Install every provider CLI when a desktop is configured for only one provider.

## Design

### 1. Add an Explicit Runtime Root

Introduce a stable provider runtime root:

```text
/opt/ai-agent-bridge/
  .nvmrc
  package.json
  package-lock.json
  node_modules/
```

The runtime root is owned by `root:root` and is writable only during
provisioning or upgrades. Provider sessions run code from repositories under
`/workspace`; they must not mutate the provider runtime.

Add a daemon flag or config field for the runtime root:

```yaml
runtime:
  provider_root: "/opt/ai-agent-bridge"
```

Use this path for Node version validation and relative provider arguments.
Do not derive runtime behavior from the daemon process working directory.

Acceptance criteria:

- Provider-enabled startup does not depend on `WorkingDirectory`.
- Node validation reads `/opt/ai-agent-bridge/.nvmrc`.
- Relative provider script paths resolve below the configured provider root.
- Existing provider-neutral configs continue to start without a runtime root.
- Unit tests cover configured, missing, and invalid runtime roots.

### 2. Package the Provider Runtime Manifest

Ship the pinned runtime manifest separately from the daemon config:

```text
/usr/share/ai-agent-bridge/provider-runtime/.nvmrc
/usr/share/ai-agent-bridge/provider-runtime/package.json
/usr/share/ai-agent-bridge/provider-runtime/package-lock.json
```

Add an idempotent install helper:

```text
/usr/lib/ai-agent-bridge/install-provider-runtime
```

The helper:

1. Installs or verifies Node.js 24.
2. Copies the packaged runtime manifest to `/opt/ai-agent-bridge`.
3. Runs `npm ci --omit=dev --no-audit --no-fund`.
4. Verifies each installed CLI version.
5. Leaves `/opt/ai-agent-bridge` owned by `root:root`.

Keep this opt-in. Installing the base `ai-agent-bridge` package must not
automatically download third-party provider CLIs.

Acceptance criteria:

- The helper is safe to run repeatedly.
- CLI versions match the repo lockfile.
- A failed install leaves the existing runtime usable.
- The helper prints installed versions and actionable errors.

### 3. Add an ai-desktops Config Profile

Add a packaged example:

```text
/usr/share/doc/ai-agent-bridge/examples/bridge-ai-desktops.yaml
```

The profile:

- binds to `127.0.0.1:9445`;
- enables persistence under `/var/lib/ai-agent-bridge`;
- permits `/workspace`;
- uses absolute provider runtime paths;
- supports enabling a selected provider subset;
- keeps TLS and JWT disabled for localhost-only use;
- documents that remote exposure requires mTLS and JWT.

Example Codex provider:

```yaml
providers:
  codex:
    binary: "/usr/bin/node"
    args: ["/opt/ai-agent-bridge/node_modules/@openai/codex/bin/codex.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["OPENAI_API_KEY"]
    prompt_pattern: "(?m)(>\\s*$|›)"
```

Acceptance criteria:

- The example config passes config validation.
- Starting the daemon with the example and selected credentials registers the
  selected providers.
- `/workspace/<repo>` passes path policy validation.

### 4. Add a systemd ai-desktops Drop-In

Keep the base systemd unit restrictive. Add a documented drop-in for desktop
hosts:

```text
/etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf
```

Contents:

```ini
[Service]
EnvironmentFile=-/etc/ai-agent-bridge/agents.env
ReadWritePaths=/var/lib/ai-agent-bridge /workspace /tmp /var/tmp
```

Do not change `WorkingDirectory` once provider runtime resolution is explicit.

The credential file must be:

```text
root:root 0600 /etc/ai-agent-bridge/agents.env
```

Acceptance criteria:

- Base package installs without granting `/workspace` access.
- Desktop provisioning installs the drop-in and restarts the daemon.
- Agent subprocesses can edit repositories under `/workspace`.
- The service account cannot write to `/opt/ai-agent-bridge`.

### 5. Integrate Provisioning Into ai-desktops

Update `ai-desktops` provisioning or AMI baking to:

1. Install the latest supported `ai-agent-bridge` apt package.
2. Run `/usr/lib/ai-agent-bridge/install-provider-runtime`.
3. Install the ai-desktops systemd drop-in.
4. Install a rendered ai-desktops bridge config.
5. Materialize provider credentials at boot from the platform secrets mechanism.
6. Run `systemctl daemon-reload`.
7. Enable and restart `ai-agent-bridge`.
8. Run a localhost bridge health check.

Credential materialization should be owned by `ai-desktops`, because it knows
the deployment environment and secret source. `ai-agent-bridge` should only
document and consume the env file contract.

Add an `ai-desktops doctor` check that reports:

- package version;
- systemd service state;
- localhost port `9445` state;
- Node.js version;
- provider runtime manifest presence;
- configured provider IDs;
- presence of required credential variable names without printing values;
- `/workspace` policy and systemd write access;
- bridge health response.

Acceptance criteria:

- A newly created desktop is ready for a configured provider without manual SSH
  setup.
- Doctor output clearly distinguishes base-package health from provider-host
  readiness.
- Re-running provisioning upgrades the runtime without deleting workspaces or
  session persistence.

### 6. Add Automated Tests

Extend packaging and integration coverage.

#### Base Apt Smoke

Retain the existing assertion:

- install package;
- start provider-neutral daemon;
- verify localhost health.

#### ai-desktops Profile Smoke

Add an Ubuntu 24.04 smoke scenario:

1. Install the apt package.
2. Install Node.js 24 and a test provider runtime.
3. Apply the ai-desktops config and systemd drop-in.
4. Create `/workspace/smoke-repo`.
5. Start the daemon.
6. Start a deterministic test provider session in `/workspace/smoke-repo`.
7. Write input and verify output replay.
8. Verify the provider can create or modify a workspace file.
9. Restart the daemon and verify persistence remains available.

Use a deterministic fixture provider in CI. Do not require external API keys for
the package smoke test.

#### Optional Live Provider Smoke

Keep real Codex, Claude, OpenCode, and Gemini tests opt-in and secret-backed.
Run them in a controlled environment after package publication.

Acceptance criteria:

- CI catches Node runtime root regressions.
- CI catches `/workspace` policy regressions.
- CI catches systemd sandbox regressions.
- CI does not require paid provider credentials for baseline coverage.

### 7. Update Documentation

Add `docs/ai-desktops.md` with:

- architecture and security boundary;
- package install and provisioning flow;
- provider runtime layout;
- environment file contract;
- example config;
- systemd drop-in;
- upgrade workflow;
- troubleshooting commands;
- localhost-only default and remote-exposure warning.

Update:

- `README.md` to link the guide;
- `docs/README.md` to index the guide;
- `docs/install-ubuntu.md` to distinguish the minimal package from the
  ai-desktops agent-host profile;
- `docs/service.md` to document `runtime.provider_root`;
- `PRD.md` to add ai-desktops as a supported deployment target.

## Implementation Sequence

### Phase 1: Bridge Runtime Contract

- [x] Add `runtime.provider_root` config support.
- [x] Resolve Node validation and relative provider arguments from that root.
- [x] Add unit tests.
- [x] Update service reference docs.

### Phase 2: Packaging

- [x] Package runtime manifests (`/usr/share/ai-agent-bridge/provider-runtime/`).
- [x] Add the idempotent provider runtime install helper (`packaging/install-provider-runtime`).
- [x] Add the ai-desktops example config.
- [x] Document the systemd drop-in.
- [x] Extend apt smoke tests (profile smoke with fixture provider + `/workspace` path policy).

### Documentation (§7)

- [x] Add `docs/ai-desktops.md`.
- [x] Update `README.md` to link the guide.
- [x] Update `docs/README.md` to index the guide.
- [x] Update `docs/install-ubuntu.md` to distinguish the minimal package from the ai-desktops profile.
- [x] Update `docs/service.md` to document `runtime.provider_root`.
- [x] Update `PRD.md` to add ai-desktops as a supported deployment target (§7.7).

### Phase 3: ai-desktops Integration

- [x] Add provisioning steps for runtime, config, credentials, and drop-in.
- [x] Add `doctor` readiness checks (`/usr/lib/ai-agent-bridge/ai-desktops-doctor`).
- [ ] Bake or provision the updated desktop image.
- [ ] Validate a fresh desktop and an upgraded existing desktop.

### Phase 4: Live Validation

- [ ] Run deterministic profile smoke on Ubuntu 24.04.
- [ ] Start a Codex session against `/workspace/<repo>`.
- [ ] Verify write access, reconnect, replay, restart, and persistence.
- [ ] Repeat for other enabled providers.

## Rollout

1. Merge and publish a bridge release with the explicit runtime-root contract.
2. Validate the apt package with base and ai-desktops profile smoke tests.
3. Update `ai-desktops` provisioning against the new bridge package version.
4. Test on a new disposable desktop.
5. Test upgrade behavior on an existing desktop.
6. Bake a new desktop AMI after both paths pass.
7. Roll out to additional desktops.

## Security Notes

- Keep the bridge on `127.0.0.1:9445` for ai-desktops unless a separate design
  adds mTLS, JWT, and an explicit network path.
- Do not log secret values or include them in doctor output.
- Keep `/etc/ai-agent-bridge/agents.env` root-owned and mode `0600`.
- Keep `/opt/ai-agent-bridge` immutable to the service account.
- Grant write access only to `/workspace`, runtime state, and temporary paths.
- Treat provider CLI upgrades as supply-chain changes: pin versions, use the
  lockfile, verify installed versions, and validate before rollout.

## Initial Target Host Evidence

The first reviewed desktop was:

```text
d-7c9d2ad7.desktops.orchael.dev
Ubuntu 24.04.4 LTS (noble)
ai-agent-bridge 0.3.3
Node.js v18.19.1
workspace: /workspace/ga-gsc-analysis
bridge: active on 127.0.0.1:9445
providers: {}
```

This host is healthy for the provider-neutral package baseline and is the first
candidate for upgrade-path validation after the bridge and ai-desktops changes
land.
