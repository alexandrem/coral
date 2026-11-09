#!/bin/bash
# Download Beyla binaries for embedding in Coral agent.
# This script is invoked by go generate before builds.

set -euo pipefail

# Beyla version to download.
# v3.0.0 includes OBI #1004 and #1071, which fix PID namespace reads
# rejected by newer kernel verifiers (including the Go ClientConn probe).
BEYLA_VERSION="${BEYLA_VERSION:-v3.0.0}"

# Get the directory where this script is located.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Output directory for downloaded binaries (relative to script location).
BINARIES_DIR="${SCRIPT_DIR}/../internal/agent/beyla/binaries"

# GitHub release URL base.
GITHUB_RELEASES="https://github.com/grafana/beyla/releases/download"

# Platform-architecture combinations to download.
# Note: Beyla is Linux-only (eBPF-based), so we only download Linux binaries.
PLATFORMS=(
    "linux-amd64"
    "linux-arm64"
)

# Create binaries directory if it doesn't exist.
mkdir -p "${BINARIES_DIR}"

# Keep archives and extracted binaries private to this invocation.
download_dir="$(mktemp -d)"
trap 'rm -rf "${download_dir}"' EXIT

echo "Downloading Beyla ${BEYLA_VERSION} binaries..."

for platform in "${PLATFORMS[@]}"; do
    output_file="${BINARIES_DIR}/beyla-${platform}"
    version_file="${output_file}.version"

    # Old downloads without a version marker must also be refreshed. Otherwise
    # changing the pin leaves local builds and Docker contexts on the old binary.
    if [ -x "${output_file}" ] && [ -f "${version_file}" ] && \
        [ "$(cat "${version_file}")" = "${BEYLA_VERSION}" ]; then
        echo "✓ beyla-${platform} ${BEYLA_VERSION} already exists, skipping"
        continue
    fi

    # Construct download URL.
    # Beyla releases use format: beyla-linux-amd64-v3.0.0.tar.gz
    archive_name="beyla-${platform}-${BEYLA_VERSION}.tar.gz"
    download_url="${GITHUB_RELEASES}/${BEYLA_VERSION}/${archive_name}"

    echo "Downloading beyla-${platform}..."

    # Download with curl (fallback to wget if curl not available).
    if command -v curl >/dev/null 2>&1; then
        curl -L -f --retry 3 --connect-timeout 10 --max-time 300 \
            -o "${download_dir}/${archive_name}" "${download_url}" || {
            echo "✗ Failed to download beyla-${platform}"
            exit 1
        }
    elif command -v wget >/dev/null 2>&1; then
        wget -O "${download_dir}/${archive_name}" "${download_url}" || {
            echo "✗ Failed to download beyla-${platform}"
            exit 1
        }
    else
        echo "✗ Neither curl nor wget found. Cannot download binaries."
        exit 1
    fi

    # Extract the binary from the tar.gz archive.
    tar -xzf "${download_dir}/${archive_name}" -C "${download_dir}" beyla || {
        echo "✗ Failed to extract beyla-${platform}"
        exit 1
    }

    # Only replace a cached binary after the download and extraction succeed.
    chmod +x "${download_dir}/beyla"
    mv "${download_dir}/beyla" "${output_file}" || {
        echo "✗ Failed to move beyla-${platform}"
        exit 1
    }
    printf '%s\n' "${BEYLA_VERSION}" > "${version_file}"

    echo "✓ Downloaded beyla-${platform}"
done

echo ""
echo "Beyla binaries downloaded to ${BINARIES_DIR}/"
echo "Total size: $(du -sh "${BINARIES_DIR}" | cut -f1)"
