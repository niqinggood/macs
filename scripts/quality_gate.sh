#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
COMPILE_ONLY=0
if [[ "${1:-}" == "--compile-only" ]]; then
  COMPILE_ONLY=1
elif [[ $# -gt 0 ]]; then
  echo "Usage: $0 [--compile-only]" >&2
  exit 2
fi

cd "${ROOT_DIR}"

echo "==> release credential guard"
go run ./cmd/releaseguard

echo "==> module checksum verification"
go mod verify

echo "==> gofmt check"
mapfile -t GO_FILES < <(
  {
    git ls-files -- '*.go'
    git ls-files --others --exclude-standard -- '*.go'
  } | sort -u
)
if (( ${#GO_FILES[@]} > 0 )); then
  declare -A LEGACY_GOFMT=()
  while IFS= read -r file; do
    [[ -z "${file}" || "${file}" == \#* ]] && continue
    LEGACY_GOFMT["${file}"]=1
  done < scripts/gofmt_legacy.txt

  mapfile -t UNFORMATTED < <(gofmt -l "${GO_FILES[@]}")
  NEW_UNFORMATTED=()
  for file in "${UNFORMATTED[@]}"; do
    if [[ -z "${LEGACY_GOFMT[${file}]+x}" ]]; then
      NEW_UNFORMATTED+=("${file}")
    fi
  done
  if (( ${#NEW_UNFORMATTED[@]} > 0 )); then
    echo "gofmt required for new or changed files:" >&2
    printf '%s\n' "${NEW_UNFORMATTED[@]}" >&2
    exit 1
  fi
  if (( ${#UNFORMATTED[@]} > 0 )); then
    echo "legacy gofmt debt: ${#UNFORMATTED[@]} file(s), no new violations"
  fi
fi

echo "==> whitespace check"
git diff --check
git diff --cached --check

echo "==> go vet"
go vet ./...

if (( COMPILE_ONLY == 1 )); then
  echo "==> package compilation"
  go test ./... -run '^$' -count=1
else
  echo "==> full Go test suite"
  go test ./... -count=1
fi

echo "==> all-package build"
go build -buildvcs=false -trimpath ./...

echo "quality gate: PASS"
