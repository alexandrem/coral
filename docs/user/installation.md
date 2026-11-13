# Installation and Permissions

Install the full `coral` CLI on the operator workstation. Run a Colony for each
application environment and agents on the Linux machines hosting its services.
The full CLI includes both server roles; separate server binaries provide a
smaller command surface for deployed hosts.

## Build from this checkout

The source build uses Go 1.25.1 or later, a C toolchain for DuckDB, and Clang/LLVM
for generated eBPF objects. Put `llvm-strip` on `PATH`. Install the repository's
generation tools with `make install-tools`; ensure the Go bin directory is also
on `PATH`. See [go.mod](../../go.mod) and the [Makefile](../../Makefile) for the
versions and build targets used by this checkout.

```bash
make install-tools
make build-all
```

Generation downloads the managed Beyla and Deno binaries, so the first build
needs network access. Outputs are under `bin/<goos>_<goarch>/`, for example
`bin/linux_amd64/coral` or `bin/darwin_arm64/coral`. Add that directory to `PATH`
or use its full path in the commands below. Keep the generated Deno companion
beside the CLI when copying a build for scripting.

```bash
coral version
coral --help
```

`make build` builds only the full CLI; `make build-colony` and `make build-agent`
build the individual server binaries. Their lifecycle commands omit the role:
`coral-colony start` corresponds to `coral colony start`, and `coral-agent start`
corresponds to `coral agent start`. Client operations use the full CLI. All
command syntax lives in the [CLI reference](../CLI_REFERENCE.md).

## Prepare the observation host

Use a Linux host or VM for eBPF collection. Kernel 5.8 or newer provides the
separate BPF and performance-monitoring capabilities; actual collection also
depends on kernel features, security policy, and process visibility. A container
uses its host's kernel, so installing an agent in a container does not supply
missing kernel facilities.

macOS requires root privileges for Colony and agent network setup and does not
provide Linux eBPF collection. Put the observation agent inside the Linux VM
running the applications when using Docker or another VM on macOS.

## Grant server privileges

The Colony needs network-administration access to create and manage its TUN
interface and routes. The agent also needs access to processes and the kernel
facilities used by its collectors. These privileges remain relevant while
services are running and probes attach.

For a local walkthrough, start the servers with `sudo` as shown in
[getting started](getting-started.md). Keep configuration accessible in that
execution context; `sudo` can discard environment variables. On Linux, a
service unit or file capabilities can grant specific privileges instead:

```bash
# Apply to separate installed binaries, using their actual paths.
sudo setcap 'cap_net_admin+ep' /usr/local/bin/coral-colony
sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf,cap_perfmon+ep' /usr/local/bin/coral-agent
getcap /usr/local/bin/coral-colony /usr/local/bin/coral-agent
```

The agent example includes `CAP_SYS_ADMIN` for namespace execution and older
kernel fallbacks. The necessary set depends on enabled features and host policy;
the [systemd unit](../../deployments/systemd/coral-agent.service) and
[Kubernetes variants](../../deployments/k8s/README.md) show deployment-specific
sets. Startup reports missing capabilities. A warning about a disabled collector
does not prove network setup or the rest of the agent succeeded.

File capabilities attach to the installed file; replacing the binary during an
upgrade can remove them. Recheck them after installation. Avoid making the full
CLI setuid: that exposes privileged execution to every user who can run it.

Querying an accessible Colony, configuring an LLM, and running the local MCP
proxy do not need root. Continue with [your first Colony](getting-started.md),
or use [deployment](deployment.md) for supervised services.
