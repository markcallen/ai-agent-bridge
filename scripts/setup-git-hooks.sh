#!/usr/bin/env bash
set -euo pipefail

pre-commit install
pre-commit install --hook-type pre-push
