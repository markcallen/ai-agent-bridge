# Lessons

## 2026-03-30 PTY Transport Test Execution
- Incident/bug: A provider unit test that executed a temp script failed inside the sandbox with `operation not permitted`.
- Root cause pattern: Tests that shell out in this environment can fail for sandbox reasons unrelated to application logic.
- Early signal missed: The first version of the startup-probe test assumed subprocess execution was always allowed in unit tests.
- Preventative rule: Keep unit tests for provider construction/path assembly pure where possible; reserve subprocess behavior checks for integration/e2e layers.
- Validation added (test/check/alert): Replaced the exec-heavy unit test with a pure command-construction test and kept PTY behavior validation in the higher-level smoke path.
