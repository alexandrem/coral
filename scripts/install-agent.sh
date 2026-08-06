#!/usr/bin/env bash
#
# Coral Agent Installer Script for Linux VPS Hosts
#
# Usage:
#   sudo ./scripts/install-agent.sh [options]
#   curl -fsSL https://raw.githubusercontent.com/coral-mesh/coral/main/scripts/install-agent.sh | sudo bash -s -- [options]
#
# Options:
#   --colony <id>         Colony ID to join
#   --fingerprint <fp>    Colony Root CA Fingerprint (sha256:...)
#   --psk <key>           Bootstrap Pre-Shared Key (coral-psk:...)
#   --discovery <url>     Discovery service URL (default: https://discovery.coral.io)
#   --agent <id>          Agent ID (default: auto-generated from hostname)
#   --version <ver>       Version tag to download (default: latest)
#   --binary-path <path>  Use local binary instead of downloading
#   --skip-service        Do not install or start systemd service
#   -h, --help            Show this help message

set -euo pipefail

REPO_OWNER="coral-mesh"
REPO_NAME="coral"
DEFAULT_DISCOVERY="https://discovery.coral.io"

COLONY_ID=""
CA_FINGERPRINT=""
BOOTSTRAP_PSK=""
DISCOVERY_URL="${DEFAULT_DISCOVERY}"
AGENT_ID=""
VERSION="latest"
BINARY_PATH=""
SKIP_SERVICE=false

log_info() {
    echo -e "\033[34m[INFO]\033[0m $*"
}

log_success() {
    echo -e "\033[32m[OK]\033[0m $*"
}

log_warn() {
    echo -e "\033[33m[WARN]\033[0m $*"
}

log_error() {
    echo -e "\033[31m[ERROR]\033[0m $*" >&2
}

usage() {
    cat <<EOF
Coral Agent VPS Installer

Usage:
  sudo $0 [options]

Options:
  --colony <id>         Colony ID to join
  --fingerprint <fp>    Colony Root CA Fingerprint (sha256:...)
  --psk <key>           Bootstrap Pre-Shared Key (coral-psk:...)
  --discovery <url>     Discovery service URL (default: ${DEFAULT_DISCOVERY})
  --agent <id>          Agent ID (default: auto-generated)
  --version <ver>       Version tag to download (default: latest)
  --binary-path <path>  Use local binary instead of downloading
  --skip-service        Do not install or start systemd service
  -h, --help            Show this help message
EOF
    exit 0
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --colony)
            COLONY_ID="$2"
            shift 2
            ;;
        --fingerprint)
            CA_FINGERPRINT="$2"
            shift 2
            ;;
        --psk)
            BOOTSTRAP_PSK="$2"
            shift 2
            ;;
        --discovery)
            DISCOVERY_URL="$2"
            shift 2
            ;;
        --agent)
            AGENT_ID="$2"
            shift 2
            ;;
        --version)
            VERSION="$2"
            shift 2
            ;;
        --binary-path)
            BINARY_PATH="$2"
            shift 2
            ;;
        --skip-service)
            SKIP_SERVICE=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            ;;
    esac
done

# Ensure running on Linux
if [[ "$(uname -s)" != "Linux" ]]; then
    log_error "This installer supports Linux OS only."
    exit 1
fi

# Ensure running as root
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root (or using sudo)."
    exit 1
fi

# Detect Architecture
ARCH=$(uname -m)
case "${ARCH}" in
    x86_64|amd64)
        GOARCH="amd64"
        ;;
    aarch64|arm64)
        GOARCH="arm64"
        ;;
    *)
        log_error "Unsupported architecture: ${ARCH}"
        exit 1
        ;;
esac

log_info "Target architecture: linux-${GOARCH}"

# Install dependencies if missing
install_dependencies() {
    log_info "Checking dependencies..."
    local pkgs=()
    command -v curl >/dev/null 2>&1 || pkgs+=("curl")
    command -v setcap >/dev/null 2>&1 || pkgs+=("libcap2-bin")
    command -v wg >/dev/null 2>&1 || pkgs+=("wireguard-tools")

    if [[ ${#pkgs[@]} -gt 0 ]]; then
        log_info "Installing missing dependencies: ${pkgs[*]}"
        if command -v apt-get >/dev/null 2>&1; then
            apt-get update -qq && apt-get install -y -qq "${pkgs[@]}"
        elif command -v dnf >/dev/null 2>&1; then
            dnf install -y -q "${pkgs[@]}"
        elif command -v yum >/dev/null 2>&1; then
            yum install -y -q "${pkgs[@]}"
        elif command -v apk >/dev/null 2>&1; then
            apk add --no-cache "${pkgs[@]}"
        fi
    fi
}

install_dependencies

# Binary setup
TARGET_BIN="/usr/local/bin/coral-agent"

if [[ -n "${BINARY_PATH}" ]]; then
    if [[ ! -f "${BINARY_PATH}" ]]; then
        log_error "Specified binary path '${BINARY_PATH}' does not exist."
        exit 1
    fi
    log_info "Copying binary from ${BINARY_PATH} to ${TARGET_BIN}..."
    cp "${BINARY_PATH}" "${TARGET_BIN}"
else
    log_info "Fetching coral-agent binary (version: ${VERSION})..."
    DOWNLOAD_URL=""
    if [[ "${VERSION}" == "latest" ]]; then
        DOWNLOAD_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/latest/download/coral-agent-linux-${GOARCH}"
    else
        DOWNLOAD_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${VERSION}/coral-agent-linux-${GOARCH}"
    fi

    log_info "Downloading from: ${DOWNLOAD_URL}"
    if ! curl -fsSL "${DOWNLOAD_URL}" -o "${TARGET_BIN}"; then
        log_error "Failed to download coral-agent binary from GitHub Releases."
        log_error "Ensure release exists or pass --binary-path to use a local build."
        exit 1
    fi
fi

chmod 755 "${TARGET_BIN}"
log_success "Binary installed to ${TARGET_BIN}"

# Set capabilities
if command -v setcap >/dev/null 2>&1; then
    log_info "Applying Linux capabilities for eBPF and WireGuard..."
    setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' "${TARGET_BIN}" || log_warn "setcap failed, systemd AmbientCapabilities will be used."
fi

# Create coral user and directories
log_info "Configuring coral system user and directories..."
if ! getent group coral >/dev/null 2>&1; then
    groupadd -r coral
fi

if ! getent passwd coral >/dev/null 2>&1; then
    useradd -r -g coral -d /var/lib/coral -s /sbin/nologin -c "Coral Agent Service" coral
fi

mkdir -p /var/lib/coral /var/log/coral /etc/coral
chown -R coral:coral /var/lib/coral /var/log/coral /etc/coral
chmod 750 /var/lib/coral /var/log/coral /etc/coral

# Certificate Bootstrap (if parameters supplied)
if [[ -n "${COLONY_ID}" && -n "${CA_FINGERPRINT}" ]]; then
    log_info "Running Certificate Bootstrap..."
    BOOTSTRAP_CMD=("${TARGET_BIN}" "bootstrap" "--colony" "${COLONY_ID}" "--fingerprint" "${CA_FINGERPRINT}")
    if [[ -n "${BOOTSTRAP_PSK}" ]]; then
        BOOTSTRAP_CMD+=("--psk" "${BOOTSTRAP_PSK}")
    fi
    if [[ -n "${DISCOVERY_URL}" ]]; then
        BOOTSTRAP_CMD+=("--discovery" "${DISCOVERY_URL}")
    fi
    if [[ -n "${AGENT_ID}" ]]; then
        BOOTSTRAP_CMD+=("--agent" "${AGENT_ID}")
    fi

    log_info "Executing bootstrap..."
    "${BOOTSTRAP_CMD[@]}" || log_warn "Bootstrap encountered issue. You can run 'sudo coral-agent bootstrap' manually later."
else
    log_warn "Colony ID or CA Fingerprint omitted. Skipping bootstrap step."
    log_warn "Run 'sudo coral-agent bootstrap --colony <ID> --fingerprint <FP>' before starting service."
fi

# Systemd Service Installation
if [[ "${SKIP_SERVICE}" == false ]] && command -v systemctl >/dev/null 2>&1; then
    log_info "Installing systemd service..."
    SERVICE_FILE="/etc/systemd/system/coral-agent.service"

    cat <<EOF > "${SERVICE_FILE}"
[Unit]
Description=Coral Agent - Unified Operations Observer
Documentation=https://coral.io/docs/agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/coral-agent start --config /etc/coral/agent.yaml
Restart=on-failure
RestartSec=10s

# Security hardening & capabilities.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/coral /var/log/coral /etc/coral
CapabilityBoundingSet=CAP_NET_ADMIN CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_SYS_RESOURCE CAP_BPF CAP_PERFMON
AmbientCapabilities=CAP_NET_ADMIN CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_SYS_RESOURCE CAP_BPF CAP_PERFMON

# Logging.
StandardOutput=journal
StandardError=journal
SyslogIdentifier=coral-agent

# Resource limits.
LimitNOFILE=65536
LimitNPROC=4096

# User and permissions.
User=coral
Group=coral

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable coral-agent.service
    log_success "Systemd service installed and enabled (coral-agent.service)."

    if [[ -n "${COLONY_ID}" && -n "${CA_FINGERPRINT}" ]]; then
        log_info "Starting coral-agent service..."
        systemctl restart coral-agent.service || log_warn "Could not start service automatically. Check systemctl status coral-agent."
    fi
fi

log_success "Coral Agent installation completed successfully!"
