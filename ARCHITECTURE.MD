# Architectural change - Colony & LLM

## 🏗️ Architectural Redesign Summary

| **Component**           | **Previous Role**                            | **New Role**                                                                                                                                                                                                | **Key Rationale**                                                                              |
|-------------------------|----------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| **Colony**              | Central LLM Host, Query Engine, Coordinator. | **Secure MCP Gateway & Control Plane:** Executes RBAC/Audit checks, issues Delegate JWTs, routes commands to Agents, and serves as the **Microservice Communication Protocol (MCP)** server for LLM Agents. | Offloads LLM compute, centralizes security checks, and enables low-latency direct connections. |
| **Reef**                | Central Dashboard, Historical Aggregation.   | **Global Aggregation & Enterprise LLM Host:** Central data store (ClickHouse/TimescaleDB), hosts the **single, dedicated enterprise-grade LLM** for global, consistent RCA and audit.                       | Ensures consistency, controls cost, and centralizes proprietary reasoning IP.                  |
| **Developer LLM Agent** | N/A (Manual Process)                         | **Local AI Reasoning Engine:** Launched via `coral ask` (or similar CLI command), runs the LLM locally (e.g., via Ollama), and connects to the Colony as a **tool-calling client**.                         | Improves developer experience (DX) and uses local compute for low-latency iteration.           |

* * *

## 🔑 Key Features and Data Flows

### 1\. Colony as the MCP Gateway

The Colony now exclusively acts as the **Control Plane** for the mesh. It exposes a standard set of
tool calls (like `issue_dynamic_probe`, `query_trace_data`) that are consumed by external LLM
agents. Every request must pass the Colony's **audit and RBAC checks**, making it the central
security enforcement point.

### 2\. Developer Empowerment (The `coral ask` Command)

The developer uses their local machine's compute power for the LLM's reasoning:

- **Local LLM Agent:** The `coral ask` command launches an agent that can host an LLM (e.g., Llama
  3) on the developer's workstation.

- **Secure Connection:** This local agent connects to the Colony via the **secure WireGuard mesh**
  and communicates using the Colony's MCP API.

- **Direct Stream:** When the LLM decides to initiate a **live probe** for RCA, the Colony issues a
  **short-lived Delegate JWT**, allowing the developer's local agent to establish a direct,
  low-latency data stream with the target **Agent** (bypassing the Colony for data flow, but not for
  authorization).

### 3\. Reef's Centralized Intelligence

The Reef is elevated to the **Global Investigation Hub** and **Enterprise LLM Service**.

- **Consistency:** By hosting a single, specialized LLM, the Reef ensures that all global
  investigations and automated RCA reports are generated using the same model and proprietary prompt
  engineering, which is crucial for enterprise-wide consistency and auditability.

- **High-Level Analysis:** The Reef is responsible for aggregating data from all Colonies for
  cross-regional correlation and providing the centralized dashboard view.
