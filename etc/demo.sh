#!/usr/bin/env sh
# Every REST operation on real state, including complete play and concurrency.
set -eu
cd "$(dirname "$0")/.."
exec python3 etc/demo.py "$@"
