#!/bin/bash
# Download Beyla binaries for embedding in Coral agent.
# This script is invoked by go generate before builds.

set -e

# Beyla version to download.
BEYLA_VERSION="${BEYLA_VERSION:-v1.8.7}"

# Output directory for downloaded binaries.
BINARIES_DIR="internal/agent/beyla/binaries"

# GitHub release URL base.
GITHUB_RELEASES="https://github.com/grafana/beyla/releases/download"

# Platform-architecture combinations to download.
PLATFORMS=(
    "linux-amd64"
    "linux-arm64"
    "darwin-amd64"
    "darwin-arm64"
)

# Create binaries directory if it doesn't exist.
mkdir -p "${BINARIES_DIR}"

echo "Downloading Beyla ${BEYLA_VERSION} binaries..."

for platform in "${PLATFORMS[@]}"; do
    output_file="${BINARIES_DIR}/beyla-${platform}"

    # Skip if already downloaded.
    if [ -f "${output_file}" ]; then
        echo "✓ beyla-${platform} already exists, skipping"
        continue
    fi

    # Construct download URL.
    # Beyla releases use format: beyla-linux-amd64, beyla-darwin-arm64, etc.
    download_url="${GITHUB_RELEASES}/${BEYLA_VERSION}/beyla-${platform}"

    echo "Downloading beyla-${platform}..."

    # Download with curl (fallback to wget if curl not available).
    if command -v curl >/dev/null 2>&1; then
        curl -L -f -o "${output_file}" "${download_url}" || {
            echo "✗ Failed to download beyla-${platform}"
            rm -f "${output_file}"
            continue
        }
    elif command -v wget >/dev/null 2>&1; then
        wget -O "${output_file}" "${download_url}" || {
            echo "✗ Failed to download beyla-${platform}"
            rm -f "${output_file}"
            continue
        }
    else
        echo "✗ Neither curl nor wget found. Cannot download binaries."
        exit 1
    fi

    # Make executable.
    chmod +x "${output_file}"

    echo "✓ Downloaded beyla-${platform}"
done

echo ""
echo "Beyla binaries downloaded to ${BINARIES_DIR}/"
echo "Total size: $(du -sh ${BINARIES_DIR} | cut -f1)"
