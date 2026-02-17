#!/bin/bash
# Wrapper for claude --print mode.
# The bridge keeps stdin open, but claude --print waits for EOF before
# processing.  Redirect stdin from /dev/null so claude sees immediate EOF
# and uses the positional argument as the prompt.
exec claude "$@" < /dev/null
