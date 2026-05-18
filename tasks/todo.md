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
