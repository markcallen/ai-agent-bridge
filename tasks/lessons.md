# Lessons

## 2026-03-30 PTY Transport Test Execution
- Incident/bug: A provider unit test that executed a temp script failed inside the sandbox with `operation not permitted`.
- Root cause pattern: Tests that shell out in this environment can fail for sandbox reasons unrelated to application logic.
- Early signal missed: The first version of the startup-probe test assumed subprocess execution was always allowed in unit tests.
- Preventative rule: Keep unit tests for provider construction/path assembly pure where possible; reserve subprocess behavior checks for integration/e2e layers.
- Validation added (test/check/alert): Replaced the exec-heavy unit test with a pure command-construction test and kept PTY behavior validation in the higher-level smoke path.

## 2026-04-23 Apt Package Smoke Harness
- Incident/bug: The first Debian package smoke test installed successfully but still failed the health check.
- Root cause pattern: Docker port publishing cannot reach a service that is intentionally bound to `127.0.0.1` inside the container.
- Early signal missed: The packaged default config is localhost-only by design, but the first smoke harness assumed host-to-container access over a published port.
- Preventative rule: When a packaged service defaults to loopback-only binding, run health verification inside the target environment or through an explicit tunnel instead of relying on Docker port publishing.
- Validation added (test/check/alert): Updated `scripts/smoke-apt-local.sh` to execute the gRPC healthcheck inside each Ubuntu container, matching packaged service behavior.
