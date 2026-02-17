#!/usr/bin/env bash
set -euo pipefail

# claude --print waits for EOF on stdin. The bridge keeps stdin open, so force
# stdin to /dev/null and rely on positional prompt args (for example arg:prompt).
exec claude "$@" < /dev/null
