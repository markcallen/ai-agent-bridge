# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

See [AGENTS.md](AGENTS.md) for the full execution framework: project structure, build/test/lint commands, coding conventions, testing guidelines, and PR guidelines.

## Claude-Specific Notes

- Never edit files under `gen/` — they are generated from `proto/`. Run `make proto` to regenerate.
- Run `make fmt` before proposing any commit; do not skip it.
- `make test` uses `-race` — do not suggest removing race detection.
- When modifying auth, session, or provider flows, always run `make test-cover` and note coverage impact.

## Installed agent rules

Created by Ballast. Do not edit this section.

Read and follow these rule files in `.claude/rules/` when they apply:

- `.claude/rules/common/local-dev-badges.md` — Rules for common/local-dev-badges
- `.claude/rules/common/local-dev-env.md` — Rules for common/local-dev-env
- `.claude/rules/common/local-dev-license.md` — Rules for common/local-dev-license
- `.claude/rules/common/local-dev-mcp.md` — Rules for common/local-dev-mcp
- `.claude/rules/common/docs.md` — Rules for common/docs
- `.claude/rules/common/cicd.md` — Rules for common/cicd
- `.claude/rules/common/observability.md` — Rules for common/observability
- `.claude/rules/common/publishing-libraries.md` — Rules for common/publishing-libraries
- `.claude/rules/common/publishing-sdks.md` — Rules for common/publishing-sdks
- `.claude/rules/common/publishing-apps.md` — Rules for common/publishing-apps
- `.claude/rules/typescript/typescript-linting.md` — Rules for typescript/linting
- `.claude/rules/typescript/typescript-logging.md` — Rules for typescript/logging
- `.claude/rules/typescript/typescript-testing.md` — Rules for typescript/testing
- `.claude/rules/go/go-linting.md` — Rules for go/linting
- `.claude/rules/go/go-logging.md` — Rules for go/logging
- `.claude/rules/go/go-testing.md` — Rules for go/testing
