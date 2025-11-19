# Coral - Product Roadmap

**Purpose**: Outlines what remains to be done for Coral to reach first viable release and beyond.
**Last Updated**: 2025-11-19

---

## Current State Summary

Coral has substantial foundation in place:
- ✅ Core mesh networking (WireGuard + Discovery service)
- ✅ Agent-Colony architecture with gRPC communication
- ✅ OTLP ingestion and Beyla integration for RED metrics
- ✅ MCP server for external tool integration
- ✅ Multi-service agent support
- ✅ DuckDB storage with time-series optimization
- ✅ Basic CLI infrastructure

**Major Gaps**:
- ❌ AI/LLM integration for debugging insights
- ❌ Dashboard UI
- ❌ Full eBPF collectors (only stubs exist)
- ❌ SDK for application integration
- ❌ Kubernetes deployment patterns
- ❌ Production hardening and security
- ❌ End-to-end testing and CI/CD

---

## Release 0.1: First Viable Product (MVP)

**Goal**: Deliver a working observability and debugging tool that developers can use locally and in production.

**Target Users**: Individual developers and small teams debugging distributed applications.

**Success Criteria**:
- Developer can set up Coral for their multi-service app in under 10 minutes
- Can observe service health, topology, and basic metrics
- Can ask natural language questions and get useful answers
- Works on macOS and Linux (development + production)
- Documented installation and usage

### Core Observability (Must-Have)

- **Complete AI integration (RFD 030)**
  - Implement `coral ask` CLI with local Genkit agent
  - Support OpenAI, Anthropic, and local Ollama models
  - Connect to Colony MCP server for data access
  - Enable multi-turn conversations
  - Cost controls and token budgets

- **Dashboard implementation**
  - Real-time service health view
  - Topology visualization (auto-discovered dependencies)
  - Metrics dashboards (RED metrics from Beyla)
  - Event timeline (deployments, restarts, errors)
  - WebSocket for live updates

- **Enhanced CLI experience**
  - Improve `coral status` with rich TUI using Bubble Tea
  - Add `coral query` commands for historical data
  - Better error messages and help text
  - Interactive setup wizard (`coral init`)
  - Shell completion support

### Data Collection & Storage

- **Complete OTLP pipeline**
  - Verify agent OTLP receivers work reliably
  - Test integration with common SDKs (Go, Python, Node.js)
  - Validate Beyla RED metrics ingestion
  - Document export configurations

- **Storage optimization**
  - Implement data retention policies
  - Add downsampling for long-term data
  - Optimize DuckDB queries for common patterns
  - Add data export capabilities (JSON, CSV)

### Deployment & Operations

- **Packaging and distribution**
  - Homebrew formula for macOS
  - Docker images for all components (colony, agent, discovery)
  - Pre-built binaries for Linux (amd64, arm64)
  - Helm chart for basic Kubernetes deployment

- **Documentation**
  - Getting started guide (15-minute tutorial)
  - Architecture overview
  - Configuration reference
  - Troubleshooting guide
  - API reference for MCP tools

- **Basic security**
  - mTLS for agent-colony communication (optional but recommended)
  - Secure storage of API keys (environment variables)
  - Input validation and sanitization
  - Audit logging for MCP tool calls

### Testing & Quality

- **Test coverage**
  - Unit tests for critical paths (>70% coverage target)
  - Integration tests for agent-colony communication
  - E2E test with sample multi-service app
  - Performance benchmarks (overhead measurement)

- **CI/CD pipeline**
  - Automated testing on PR
  - Binary builds for releases
  - Docker image publishing
  - Release notes generation

---

## Release 0.2: Production Ready

**Goal**: Harden Coral for production use with improved reliability, security, and operational features.

**Target Users**: Teams running production distributed systems, SRE teams, platform engineers.

**Success Criteria**:
- Runs reliably in production for 30+ days without issues
- Handles 100+ services across multiple hosts
- Security best practices implemented and documented
- Incident response features are useful during real outages

### Advanced Observability

- **Full eBPF implementation (RFD 013)**
  - Real eBPF programs using libbpf (not stubs)
  - HTTP latency collector
  - CPU profiling collector
  - TCP metrics collector
  - Syscall stats collector
  - Container-aware symbolization

- **Advanced querying**
  - Complex correlation queries across services
  - Time-range comparisons (now vs. 1 hour ago)
  - Cross-service dependency analysis
  - Anomaly detection and alerting
  - Custom metric aggregations

### Kubernetes Integration

- **Kubernetes deployment patterns (RFD 012)**
  - DaemonSet mode for node-wide visibility
  - Sidecar mode for pod-scoped monitoring
  - Automatic pod discovery via K8s API
  - Remote operations (`coral shell`, `coral exec`)
  - Multi-tenant cluster support

- **K8s-specific features**
  - Pod lifecycle tracking
  - Resource utilization per pod/namespace
  - Service mesh integration (Istio, Linkerd)
  - Custom Resource Definitions (CRDs)
  - Operator pattern for colony management

### Security & Hardening

- **Production security (RFD 020, 022)**
  - Mandatory mTLS for all mesh communication
  - Certificate rotation with Step CA
  - RBAC for MCP tools (read-only vs. admin)
  - Secure credential storage (keyring integration)
  - Audit logging for all operations

- **Operational hardening**
  - Health check endpoints for all components
  - Graceful shutdown and restart
  - Resource limits and quotas (CPU, memory, storage)
  - Rate limiting for API endpoints
  - Circuit breakers for external dependencies

### Developer Experience

- **SDK development**
  - Go SDK (`github.com/coral-io/coral-go`)
  - Python SDK
  - Feature flag integration
  - Traffic sampling hooks
  - Custom health checks
  - Deployment event reporting

- **Improved debugging tools**
  - Live tail of service logs
  - Request tracing across services
  - Performance profiling on-demand
  - Shell access to running containers
  - Traffic inspection and replay

---

## Release 0.3: Multi-Environment & Scale

**Goal**: Enable cross-environment correlation and support larger deployments.

**Target Users**: Organizations with multiple environments (dev/staging/prod), large-scale deployments.

**Success Criteria**:
- Can correlate metrics across environments (prod vs. staging)
- Supports 500+ services across multiple clusters
- Minimal performance overhead (<2% CPU, <100MB RAM per agent)
- Reef enables organization-wide insights

### Multi-Colony Federation

- **Reef implementation (RFD 003)**
  - ClickHouse storage for federated data
  - Cross-colony correlation engine
  - Server-side LLM for unified analysis
  - Deployment timeline tracking
  - Public HTTPS endpoint for external integrations

- **Federation features**
  - Environment comparison (prod vs. staging)
  - Deployment impact analysis
  - Cross-app dependency graphs
  - Predictive monitoring (staging → prod)
  - Fleet-wide health aggregation

### Scale & Performance

- **Horizontal scaling**
  - Colony HA with leader election
  - Agent load balancing
  - Discovery service clustering
  - DuckDB replication options
  - Sharding strategies for large deployments

- **Performance optimization**
  - Query optimization for large datasets
  - Efficient data compression
  - Background aggregation jobs
  - Query result caching
  - Incremental data collection

### Advanced AI Features

- **Predictive intelligence**
  - Capacity planning recommendations
  - Failure prediction based on patterns
  - Deployment risk assessment
  - Automated root cause analysis
  - Historical pattern matching

- **Integration ecosystem**
  - Slack/Discord notifications
  - GitHub Actions integration
  - PagerDuty bidirectional sync
  - Grafana data source plugin
  - Prometheus remote write

---

## Release 0.4+: Advanced Features

**Goal**: Differentiate with unique capabilities that larger observability tools don't offer.

**Future consideration - depends on user feedback and demand.**

### Potential Features

- **Graduated autonomy (RFD Phase 5)**
  - Supervised automation with human approval
  - Auto-remediation for common issues
  - Progressive rollout automation
  - Self-healing capabilities
  - Constrained autonomy with safety rails

- **Advanced eBPF capabilities**
  - Live debugging with uprobes
  - Traffic capture and replay
  - Security monitoring (syscall anomalies)
  - Performance profiling (memory, I/O)
  - Custom eBPF program deployment

- **Developer productivity tools**
  - IDE integration (VS Code extension)
  - Local development mode
  - Time-travel debugging
  - Production data sampling for local testing
  - Service blueprint generation

- **Compliance & governance**
  - SOC2 audit trail
  - GDPR compliance features
  - Data residency controls
  - Retention policy enforcement
  - Change approval workflows

---

## Prioritization Framework

Features are prioritized based on:

1. **User Impact** (High/Medium/Low)
   - How many users benefit?
   - How much time does it save?
   - Does it enable new workflows?

2. **Technical Dependencies**
   - What must be done first?
   - What are the blockers?
   - Can it be done incrementally?

3. **Risk & Complexity**
   - How complex is the implementation?
   - What could go wrong?
   - What are the unknowns?

4. **Strategic Value**
   - Does it differentiate from competitors?
   - Does it enable future features?
   - Does it expand the addressable market?

---

## Detailed Breakdown by Component

### 1. AI & Intelligence (High Priority)

**Release 0.1**:
- [ ] Implement Genkit Go integration
- [ ] Build `coral ask` CLI command
- [ ] Connect to Colony MCP server as client
- [ ] Support OpenAI, Anthropic, Ollama providers
- [ ] Configuration system for API keys
- [ ] Cost tracking and budgets
- [ ] Multi-turn conversation support
- [ ] Testing with realistic scenarios

**Release 0.2**:
- [ ] Improve prompt engineering for better answers
- [ ] Add caching for common queries
- [ ] Implement feedback loop (thumbs up/down)
- [ ] Context-aware suggestions
- [ ] Integration with external knowledge bases

**Release 0.3**:
- [ ] Reef-level AI (cross-environment analysis)
- [ ] Predictive insights
- [ ] Automated correlation detection
- [ ] Historical pattern matching
- [ ] Impact analysis for deployments

### 2. Dashboard & Visualization (High Priority)

**Release 0.1**:
- [ ] React or Svelte frontend framework setup
- [ ] Service health overview page
- [ ] Topology graph with D3.js
- [ ] RED metrics dashboards (Beyla data)
- [ ] Event timeline
- [ ] WebSocket for real-time updates
- [ ] Responsive design (mobile-friendly)
- [ ] Dark mode support

**Release 0.2**:
- [ ] Custom dashboard builder
- [ ] Saved queries and views
- [ ] Alert configuration UI
- [ ] User preferences and settings
- [ ] Export dashboards as images/PDFs
- [ ] Collaboration features (sharing views)

**Release 0.3**:
- [ ] Multi-colony view
- [ ] Cross-environment comparison charts
- [ ] Deployment impact visualization
- [ ] Advanced filtering and grouping
- [ ] Custom metric widgets

### 3. eBPF Collectors (Medium Priority)

**Release 0.1**:
- [ ] Skip - use Beyla for RED metrics
- [ ] Focus on MCP tools and AI integration

**Release 0.2**:
- [ ] Implement libbpf integration
- [ ] HTTP latency collector (kernel space)
- [ ] CPU profiling with stack traces
- [ ] TCP metrics (retransmits, RTT)
- [ ] Syscall stats collector
- [ ] Symbolization (ELF/DWARF parsing)
- [ ] Container-aware symbol resolution
- [ ] Resource limits and safety checks

**Release 0.3**:
- [ ] Custom eBPF program loader
- [ ] Security monitoring collectors
- [ ] Network flow analysis
- [ ] I/O latency tracking
- [ ] Memory allocation profiling

### 4. Kubernetes Support (Medium Priority)

**Release 0.1**:
- [ ] Basic Helm chart for deployment
- [ ] Docker images for all components
- [ ] DaemonSet deployment example
- [ ] Sidecar injection example (manual)

**Release 0.2**:
- [ ] DaemonSet mode with auto-discovery
- [ ] Sidecar mode with shareProcessNamespace
- [ ] Mutating webhook for auto-injection
- [ ] CRD for Colony configuration
- [ ] Operator for lifecycle management
- [ ] Multi-tenant support
- [ ] RBAC integration

**Release 0.3**:
- [ ] Fleet management for multi-cluster
- [ ] Cluster federation
- [ ] Cross-cluster service mesh
- [ ] GitOps integration (ArgoCD, Flux)
- [ ] Custom scheduler integration

### 5. SDK (Medium Priority)

**Release 0.1**:
- [ ] Skip - focus on passive observation
- [ ] Document SDK integration patterns

**Release 0.2**:
- [ ] Go SDK (`github.com/coral-io/coral-go`)
  - [ ] Service registration
  - [ ] Health checks
  - [ ] Custom metrics
  - [ ] Feature flags
  - [ ] Traffic sampling
  - [ ] Deployment events
- [ ] Python SDK (if demand exists)
- [ ] Examples and documentation

**Release 0.3**:
- [ ] Node.js SDK
- [ ] Java SDK
- [ ] Advanced SDK features (circuit breakers, retries)
- [ ] Framework integrations (Gin, Echo, FastAPI)

### 6. Security & Production Hardening (High Priority)

**Release 0.1**:
- [ ] Environment variable configuration for secrets
- [ ] Basic input validation
- [ ] MCP tool audit logging
- [ ] Secure defaults

**Release 0.2**:
- [ ] mTLS implementation (RFD 020)
- [ ] Step CA integration (RFD 022)
- [ ] Certificate rotation
- [ ] RBAC for MCP tools
- [ ] Keyring integration for secrets
- [ ] Security audit and penetration testing
- [ ] Vulnerability scanning in CI

**Release 0.3**:
- [ ] SOC2 compliance features
- [ ] Advanced audit logging
- [ ] Compliance reporting
- [ ] Data residency controls
- [ ] Encryption at rest

### 7. Reef Multi-Colony Federation (Low Priority - Future)

**Release 0.3**:
- [ ] ClickHouse storage setup
- [ ] Colony→Reef data ingestion
- [ ] Cross-colony correlation queries
- [ ] Deployment timeline tracking
- [ ] Public HTTPS endpoint
- [ ] API token authentication
- [ ] JWT for user sessions
- [ ] RBAC for Reef access

**Release 0.4**:
- [ ] Server-side LLM service
- [ ] Automated correlation analysis
- [ ] Predictive monitoring
- [ ] External integrations (Slack, GitHub Actions)
- [ ] Multi-reef federation

### 8. Documentation & Developer Experience (High Priority)

**Release 0.1**:
- [ ] Getting started guide
- [ ] Installation instructions (macOS, Linux, Docker, K8s)
- [ ] Quick start tutorial (15 minutes)
- [ ] Architecture documentation
- [ ] Configuration reference
- [ ] Troubleshooting guide
- [ ] FAQ

**Release 0.2**:
- [ ] Best practices guide
- [ ] Production deployment guide
- [ ] Security hardening guide
- [ ] Performance tuning guide
- [ ] Migration guide from other tools
- [ ] Video tutorials

**Release 0.3**:
- [ ] API reference documentation
- [ ] SDK documentation
- [ ] Plugin development guide
- [ ] Contributing guide
- [ ] Community resources

### 9. Testing & Quality (High Priority)

**Release 0.1**:
- [ ] Unit tests for core packages (>70% coverage)
- [ ] Integration tests for agent-colony communication
- [ ] E2E test with sample app (3-5 services)
- [ ] Performance benchmarks
- [ ] Load testing (100 agents)
- [ ] CI/CD pipeline (GitHub Actions)

**Release 0.2**:
- [ ] Chaos testing (network failures, restarts)
- [ ] Stress testing (1000+ services)
- [ ] Security testing (fuzzing, penetration)
- [ ] Upgrade testing (version compatibility)
- [ ] Regression test suite

**Release 0.3**:
- [ ] Automated performance regression detection
- [ ] Multi-environment test matrix
- [ ] Customer scenario testing
- [ ] Reliability testing (30+ day runs)

### 10. Packaging & Distribution (High Priority)

**Release 0.1**:
- [ ] Pre-built binaries (Linux amd64, arm64, macOS)
- [ ] Docker images (colony, agent, discovery)
- [ ] Docker Compose example
- [ ] Homebrew formula
- [ ] Basic Helm chart
- [ ] Installation script

**Release 0.2**:
- [ ] APT repository (Debian/Ubuntu)
- [ ] RPM repository (RHEL/CentOS)
- [ ] Snap package
- [ ] Helm repository
- [ ] OCI artifacts
- [ ] Auto-update mechanism

**Release 0.3**:
- [ ] AWS Marketplace listing
- [ ] GCP Marketplace listing
- [ ] Azure Marketplace listing
- [ ] Terraform modules
- [ ] CloudFormation templates

---

## What We Won't Build (Scope Boundaries)

To maintain focus and avoid feature creep, Coral will NOT:

1. **Replace full observability platforms**
   - No long-term metrics storage (use Prometheus/Grafana)
   - No log aggregation platform (use ELK, Loki)
   - No APM platform (use Datadog, New Relic)
   - Position: Complement existing tools with AI-powered debugging

2. **Become a deployment tool**
   - No CI/CD pipelines (use GitHub Actions, Jenkins)
   - No infrastructure provisioning (use Terraform, Pulumi)
   - No configuration management (use Ansible, Chef)
   - Position: Observe and debug deployments, not orchestrate them

3. **Provide incident management**
   - No on-call scheduling (use PagerDuty, Opsgenie)
   - No incident ticketing (use Jira, Linear)
   - No runbook execution (use Rundeck)
   - Position: Integrate with incident tools via MCP/API

4. **Support every language/framework**
   - Focus on Go, Python, Node.js for SDKs
   - Other languages rely on passive observation + OTLP
   - Position: Deep integration for popular languages, broad passive support

5. **Build a SaaS platform (initially)**
   - Self-hosted first (developer machines, VMs, K8s)
   - Cloud offering only after PMF validation
   - Position: Developer-owned and controlled

---

## Success Metrics

### Release 0.1 Success Criteria
- 100+ active users (developers using it weekly)
- <5 minute setup time (from download to first query)
- <10% resource overhead (CPU + memory on agents)
- 80%+ user satisfaction ("useful for debugging")
- 5+ public testimonials or case studies

### Release 0.2 Success Criteria
- 500+ active users
- Running in production at 10+ companies
- 99.9% uptime (colony availability)
- <1% false positive rate (AI recommendations)
- Security audit completed with no critical issues

### Release 0.3 Success Criteria
- 2000+ active users
- Multi-environment usage at 50+ companies
- Scales to 1000+ services per colony
- Active community (Discord, GitHub discussions)
- 10+ third-party integrations

---

## Release Timeline (Estimates)

**Release 0.1 (MVP)**: 12-16 weeks from now
- Weeks 1-4: AI integration + basic dashboard
- Weeks 5-8: Documentation + packaging
- Weeks 9-12: Testing + polish
- Weeks 13-16: Beta testing + bug fixes

**Release 0.2 (Production Ready)**: 6-8 months after 0.1
- Focus on stability, security, K8s support
- Continuous improvements based on user feedback

**Release 0.3 (Multi-Environment)**: 12-15 months after 0.1
- Reef implementation
- Advanced features
- Scale optimization

---

## How to Contribute

See individual RFD documents for detailed implementation specs:
- RFD 030: `coral ask` implementation
- RFD 013: eBPF collectors
- RFD 012: Kubernetes deployment
- RFD 003: Reef federation

Current priorities are tracked in GitHub Projects and Issues.

---

## Related Documents

- [CONCEPT.md](docs/CONCEPT.md) - Product vision and philosophy
- [ARCHITECTURE.md](docs/ARCHITECTURE.md) - System architecture
- [IMPLEMENTATION.md](docs/IMPLEMENTATION.md) - Technical implementation
- [RFDs/](RFDs/) - Detailed design documents
