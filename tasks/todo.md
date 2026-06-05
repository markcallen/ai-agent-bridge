# Issue 57 Implementation Plan

## Governing PRD

- Update `PRD.md` with a Debian/Ubuntu distribution section covering package contents, supported Ubuntu releases, apt repository hosting, signing, installation flow, and smoke-test evidence requirements.

## Scope

- Add Debian packaging for `ai-agent-bridge` using `nfpm`.
- Extend the release workflow to build `.deb` artifacts, publish them into a signed apt repository, and attach release artifacts.
- Add an `install.sh` helper and Ubuntu installation docs.
- Add packaging-focused tests and smoke coverage.
- Add an EC2 smoke test flow that provisions a host, installs the apt package, validates the service, and tears the host down.

## Constraints

- Do not replace the existing tag-driven `publish.yml` workflow; extend it.
- Keep package contents honest: ship the bridge binaries, systemd unit, and default config, but document provider CLIs as separate runtime prerequisites.
- Support Ubuntu `24.04 noble` and `26.04 resolute` for the initial implementation.
- Keep the apt repo hosting inside GitHub-native infrastructure.

## Tradeoffs

- Prefer `nfpm` over `dpkg-buildpackage` to keep packaging metadata small and repo-local.
- Prefer GitHub Pages for the apt repo over third-party hosted repositories to avoid new vendor dependencies.
- Limit package architectures to `amd64` first; defer `arm64` until the packaging path is proven.

## Risks

- GPG signing and apt metadata generation can fail in CI in ways that are hard to diagnose without explicit smoke coverage.
- Systemd behavior differs between container tests and real hosts, so an EC2 validation step is needed for service verification.
- The existing daemon expects external provider CLIs and secrets; the package and service docs must not imply a turnkey production install.

## Test Strategy

- Add unit coverage for generated packaging metadata helpers where practical.
- Add workflow-level smoke commands that build the `.deb`, generate repo metadata, and install from the local repo inside Ubuntu containers.
- Add an EC2 smoke script/workflow that installs from the published apt repo and validates the systemd service and gRPC health path.

## Rollback Strategy

- Revert the apt publish job from `publish.yml`.
- Remove the published apt repo branch contents or stop updating them.
- Users can continue using the existing GitHub release and container installation paths.

## Execution Checklist

- [x] Update `PRD.md` with apt distribution requirements and acceptance criteria.
- [x] Add the smallest failing packaging test(s) for required release metadata/files.
- [x] Add packaging assets (`nfpm`, systemd unit, config/install assets).
- [x] Extend release automation to build `.deb`, sign/publish apt metadata, and upload artifacts.
- [x] Add local/container smoke coverage for install-from-repo.
- [x] Add EC2 smoke automation for install and service validation.
- [x] Update `README.md` and docs for Ubuntu installation and runtime expectations.
- [x] Run targeted verification and capture evidence.
- [x] Record any new lessons in `tasks/lessons.md`.

---

# CLI Security Follow-ups (from PR #92 Copilot review)

## Deferred — cert lifecycle

- [ ] **Cert renewal**: Leaf certs (server, local-client) have 90-day validity but `EnsurePKI` uses `ca.crt` as sentinel and never regenerates them. Add expiry check (e.g. warn at 14 days, auto-regenerate at expiry) so secure mode doesn't silently break after 90 days.
- [ ] **SAN mismatch detection**: If the user restarts with different `--san` values, the existing server cert is reused even though its SANs don't cover the new names. Compare requested SANs against the cert's actual SANs and regenerate if they differ.

## Lower priority

- [ ] **Windows status message**: `server.go:71` says "unix socket" in local mode but Windows uses TCP localhost. Make the status text platform-aware.
- [ ] **pki_test.go portability**: `TestLoadPKIMaterial` hard-codes Unix-style `/tmp/...` paths. Use `filepath.Join` so it passes on Windows.
- [ ] **TestMain cleanup**: `defer os.RemoveAll(dir)` in `TestMain` never runs because `os.Exit(m.Run())` terminates first. Capture exit code, clean up, then exit.
- [ ] **Echo test assertion**: `TestSessionAttachAndInput` conditionally asserts `if got != ""` — silently passes when echo fails. Should require the expected output.

## Deferred — pre-existing / out of scope

- [ ] **GoReleaser Windows target**: `.goreleaser.yaml` includes `windows` for the CLI but `internal/provider/stdio.go` uses Unix-only APIs (`syscall.Kill`, `creack/pty`). Either add Windows build tags to the provider package or remove the Windows release target. (Pre-existing on parent branch.)
- [ ] **Docker E2E cleanup trap**: `scripts/test-cli-e2e-docker.sh` doesn't install a trap for Ctrl-C — compose stack can be left behind on interrupt.
