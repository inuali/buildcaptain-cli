#!/usr/bin/env bash
# Builds a local ./buildcaptain binary with the version stamped from git.
set -euo pipefail
cd "$(dirname "$0")"

version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
go build -trimpath -ldflags "-s -w -X main.version=${version}" -o buildcaptain .
echo "Built ./buildcaptain (${version})"
