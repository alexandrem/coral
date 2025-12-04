---
name: Bug report
about: Report a bug in Coral
title: '[BUG] '
labels: bug
assignees: ''

---

**Component**
Which component is affected?
- [ ] Colony (coordinator)
- [ ] Agent (local observer)
- [ ] SDK
- [ ] Discovery service
- [ ] CLI (`coral` binary)
- [ ] Other (specify):

**Describe the bug**
A clear and concise description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior:
1. Start colony with `coral colony start ...`
2. Connect agent with `coral connect ...`
3. Run command `...`
4. Observe error

**Expected behavior**
What you expected to happen.

**Environment**
- OS: [e.g., macOS 14, Ubuntu 22.04]
- Coral Version: [run `coral version`]
- Go Version: [run `go version`]
- Deployment: [e.g., single machine, multi-host, Docker]
- Number of agents: [e.g., 1, 5, 10+]

**Logs**
Please attach relevant logs (sanitize sensitive data):
- Colony logs: [if applicable]
- Agent logs: [if applicable]
- Discovery service logs: [if applicable]
- WireGuard status: [run `wg show` if network-related]

**Network Configuration**
- Network topology: [e.g., all local, cross-datacenter]
- Firewall/NAT: [any restrictions?]

**Additional context**
Add any other context, screenshots of terminal output, or configuration files.
