#!/bin/bash
# Download Deno binaries for embedding in Coral CLI.
# This script is invoked by go generate before builds.

set -e

# Deno version to download.
DENO_VERSION="${DENO_VERSION:-2.0.6}"

# Get the directory where this script is located.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Output directory for downloaded binaries (relative to script location).
BINARIES_DIR="${SCRIPT_DIR}/../internal/cli/run/binaries"

# GitHub release URL base.
GITHUB_RELEASES="https://github.com/denoland/deno/releases/download"

# Platform-architecture combinations to download.
# Format: "os-arch|deno-platform-string"
PLATFORMS=(
    "linux-amd64|x86_64-unknown-linux-gnu"
    "linux-arm64|aarch64-unknown-linux-gnu"
    "darwin-amd64|x86_64-apple-darwin"
    "darwin-arm64|aarch64-apple-darwin"
)

# Create binaries directory if it doesn't exist.
mkdir -p "${BINARIES_DIR}"

echo "Downloading Deno ${DENO_VERSION} binaries..."

for platform_pair in "${PLATFORMS[@]}"; do
    # Split the pair
    IFS='|' read -r platform deno_platform <<< "$platform_pair"

    output_file="${BINARIES_DIR}/deno-${platform}"

    # Skip if already downloaded.
    if [ -f "${output_file}" ]; then
        echo "✓ deno-${platform} already exists, skipping"
        continue
    fi

    # Construct download URL.
    # Deno releases use format: deno-x86_64-unknown-linux-gnu.zip
    archive_name="deno-${deno_platform}.zip"
    download_url="${GITHUB_RELEASES}/v${DENO_VERSION}/${archive_name}"

    echo "Downloading deno-${platform}..."

    # Download with curl (fallback to wget if curl not available).
    if command -v curl >/dev/null 2>&1; then
        curl -L -f -o "/tmp/${archive_name}" "${download_url}" || {
            echo "✗ Failed to download deno-${platform} from ${download_url}"
            rm -f "/tmp/${archive_name}"
            continue
        }
    elif command -v wget >/dev/null 2>&1; then
        wget -O "/tmp/${archive_name}" "${download_url}" || {
            echo "✗ Failed to download deno-${platform}"
            rm -f "/tmp/${archive_name}"
            continue
        }
    else
        echo "✗ Neither curl nor wget found. Cannot download binaries."
        exit 1
    fi

    # Extract the binary from the zip archive.
    unzip -q -o "/tmp/${archive_name}" -d "/tmp" || {
        echo "✗ Failed to extract deno-${platform}"
        rm -f "/tmp/${archive_name}"
        continue
    }

    # Move the binary to the binaries directory.
    mv "/tmp/deno" "${output_file}" || {
        echo "✗ Failed to move deno-${platform}"
        rm -f "/tmp/${archive_name}"
        continue
    }

    # Clean up the archive.
    rm -f "/tmp/${archive_name}"

    # Make executable.
    chmod +x "${output_file}"

    echo "✓ Downloaded deno-${platform}"
done

echo ""
echo "Deno binaries downloaded to ${BINARIES_DIR}/"
echo "Total size: $(du -sh ${BINARIES_DIR} 2>/dev/null | cut -f1 || echo 'N/A')"
