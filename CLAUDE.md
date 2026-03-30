# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

See [AGENTS.md](AGENTS.md) for the full execution framework: project structure, build/test/lint commands, coding conventions, testing guidelines, and PR guidelines.

## Claude-Specific Notes

- Never edit files under `gen/` — they are generated from `proto/`. Run `make proto` to regenerate.
- Run `make fmt` before proposing any commit; do not skip it.
- `make test` uses `-race` — do not suggest removing race detection.
- When modifying auth, session, or provider flows, always run `make test-cover` and note coverage impact.
