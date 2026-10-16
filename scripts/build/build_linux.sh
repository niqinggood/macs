#!/usr/bin/env bash
# Build self-contained OpenKitty Linux binaries and edge-device bundles.
# Usage: ./scripts/build/build_linux.sh [all|amd64|arm64] [output-directory]

set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET="${1:-all}"
OUTPUT_DIR="${2:-${ROOT_DIR}/dist}"

case "${TARGET}" in
  all)
    ARCHITECTURES=(amd64 arm64)
    ;;
  amd64|arm64)
    ARCHITECTURES=("${TARGET}")
    ;;
  *)
    echo "Unsupported architecture: ${TARGET}" >&2
    echo "Usage: $0 [all|amd64|arm64] [output-directory]" >&2
    exit 2
    ;;
esac

cd "${ROOT_DIR}"
mkdir -p "${OUTPUT_DIR}"

if [[ "${OPENKITTY_SKIP_QUALITY_GATE:-0}" != "1" ]]; then
  bash "${ROOT_DIR}/scripts/quality_gate.sh"
elif [[ "${OPENKITTY_SKIP_RELEASE_GUARD:-0}" != "1" ]]; then
  go run ./cmd/releaseguard
fi

VERSION="${OPENKITTY_VERSION:-}"
if [[ -z "${VERSION}" ]]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || true)"
fi
VERSION="${VERSION:-dev}"
VERSION="${VERSION// /-}"
BUILD_DATE="${OPENKITTY_BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.CurrentVersion=${VERSION} -X main.build=${BUILD_DATE}"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "sha256sum or shasum is required" >&2
    return 1
  fi
}

verify_binary() {
  local binary="$1"
  local architecture="$2"
  local metadata

  metadata="$(go version -m "${binary}")"
  grep -Eq 'build[[:space:]]+GOOS=linux' <<<"${metadata}"
  grep -Eq "build[[:space:]]+GOARCH=${architecture}" <<<"${metadata}"

  if command -v file >/dev/null 2>&1; then
    local description
    description="$(file -b "${binary}")"
    case "${architecture}" in
      amd64)
        grep -Eqi 'x86-64|x86_64' <<<"${description}"
        ;;
      arm64)
        grep -Eqi 'aarch64|arm64' <<<"${description}"
        ;;
    esac
  fi
}

package_bundle() {
  local architecture="$1"
  local binary="$2"
  local checksum="$3"
  local bundle_dir="${OUTPUT_DIR}/linux-${architecture}"
  local archive="${OUTPUT_DIR}/openkitty-edge_linux_${architecture}.tar.gz"

  mkdir -p "${bundle_dir}"
  cp "${binary}" "${bundle_dir}/openkitty"
  chmod 0755 "${bundle_dir}/openkitty"
  cp "${ROOT_DIR}/deploy/install.sh" "${bundle_dir}/install.sh"
  cp "${ROOT_DIR}/deploy/openkitty.service" "${bundle_dir}/openkitty.service"
  cp "${ROOT_DIR}/deploy/openkitty.env.example" "${bundle_dir}/openkitty.env.example"
  cp "${ROOT_DIR}/openkitty-config.template.yaml" "${bundle_dir}/openkitty-config.template.yaml"
  for legal_file in LICENSE NOTICE COMMERCIAL-LICENSE.md TRADEMARKS.md BRANDING.md THIRD-PARTY-NOTICES.md; do
    cp "${ROOT_DIR}/${legal_file}" "${bundle_dir}/${legal_file}"
  done
  mkdir -p "${bundle_dir}/third_party"
  cp -R "${ROOT_DIR}/third_party/." "${bundle_dir}/third_party/"
  printf '%s  openkitty\n' "${checksum}" >"${bundle_dir}/SHA256SUMS"

  tar -C "${bundle_dir}" -czf "${archive}" \
    openkitty install.sh openkitty.service openkitty.env.example \
    openkitty-config.template.yaml SHA256SUMS \
    LICENSE NOTICE COMMERCIAL-LICENSE.md TRADEMARKS.md BRANDING.md \
    THIRD-PARTY-NOTICES.md third_party
  echo "  bundle: ${archive}"
}

echo "Building OpenKitty Linux edge binaries"
echo "  version: ${VERSION}"
echo "  build:   ${BUILD_DATE}"

for architecture in "${ARCHITECTURES[@]}"; do
  binary="${OUTPUT_DIR}/openkitty_linux_${architecture}"
  echo "  target:  linux/${architecture}"

  CGO_ENABLED=0 GOOS=linux GOARCH="${architecture}" \
    go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o "${binary}" ./cmd/macs

  verify_binary "${binary}" "${architecture}"
  checksum="$(sha256_file "${binary}")"
  printf '%s  %s\n' "${checksum}" "$(basename "${binary}")" >"${binary}.sha256"
  package_bundle "${architecture}" "${binary}" "${checksum}"
done

echo "Linux edge artifacts are ready in: ${OUTPUT_DIR}"
