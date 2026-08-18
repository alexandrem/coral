---
rfd: "113"
title: "External Assistant Integration via MCP"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "004", "094", "100" ]
database_migrations: [ ]
areas: [ "cli", "mcp", "terminal", "docs" ]
---

# RFD 113 - External Assistant Integration via MCP

**Status:** 🚧 Draft

## Summary

Coral's MCP server (RFD 004) already lets any MCP-capable client drive Coral,
but onboarding only emits Claude Desktop JSON and cannot verify a registration.
This RFD adds native registration output for Claude Code, Codex, and Gemini
CLI, plus a read-only-by-default `coral mcp doctor` diagnostic. It also
adds an explicit `coral terminal` external-assistant mode that bypasses
embedded LLM setup and prints launch instructions for a registered native
client without spawning it.

## Problem

- **Current behavior/limitations**: `coral mcp configure` hardcodes Claude Desktop's `mcpServers` JSON
  and tells the user to copy it into a config file by hand. There is no
  `--client` flag, no support for other MCP-capable CLIs (Claude Code, Codex,
  Gemini CLI), and no way to confirm registration succeeded short of opening
  the client and trying it.
- **Why this matters**: Users who already run Claude Code, Codex, or Gemini
  CLI have no low-friction path to point that assistant at Coral. They either
  fall back to `coral ask` (which needs their own API key) or hand-edit
  JSON/TOML config files by trial and error, with no feedback when it's wrong.
  The auth model differs per client and this RFD must not blur it: Claude
  Code and Codex both support a personal subscription plan, but as of Google's
  June 18, 2026 change, Gemini CLI no longer serves individual, free, or
  Google AI Pro/Ultra accounts — only API-key and enterprise auth remain
  ([announcement](https://github.com/google-gemini/gemini-cli/discussions/28017)).
  Registration output and documentation must say "API key / enterprise" for
  Gemini, never "subscription." That same announcement directs individual
  users toward Google's separate Antigravity CLI; this RFD deliberately does
  not add an Antigravity adapter (see Key Design Decisions) and Gemini CLI
  support here should not be read as covering it.
- **Use cases affected**: A user who wants their existing assistant (not a
  separate API key) to debug through Coral has to reverse-engineer their
  client's MCP config format and has no way to check the registration is
  live before starting a session. `coral terminal` compounds this — it only
  offers the embedded BYO-API conversation, with no signpost that handing off
  to an already-running external assistant is a separate, valid path.

## Solution

**Key Design Decisions:**

- **External clients, not LLM providers.** Coral never reads or reuses vendor
  credentials. Claude Code, Codex, and Gemini CLI authenticate themselves and
  use Coral as an MCP tool server; embedded generation remains API-backed.
- **Client adapters generate native registration commands.** Claude Code,
  Codex, and Gemini CLI use their `mcp add` commands; Claude Desktop retains
  the existing JSON snippet. Scope is explicit where supported (Claude:
  `local|project|user`; Gemini: `project|user`) and invalid combinations fail
  clearly. Gemini is labelled API-key/enterprise only, not subscription.
- **MCP is a top-level public command group.** The canonical interface is
  `coral mcp configure`, `coral mcp doctor`, `coral mcp proxy`, and (while it
  remains useful after RFD 100) `coral mcp list-tools`. The old `coral colony
  mcp generate-config` and `coral colony mcp proxy` paths remain hidden or
  deprecated compatibility aliases, and `coral assistant doctor` remains an
  alias for existing scripts. Newly generated registrations always use
  `coral mcp proxy`, but registration detection accepts both proxy paths so
  existing Claude Desktop configuration continues to work.
- **Embedded and external terminal modes stay separate.** Embedded chat is
  the unchanged default and needs its existing LLM configuration. Explicit
  external-assistant mode bypasses that setup and prints launch instructions
  for a registered native client — it does not spawn or exec one (see
  Component Changes; direct process handoff is Future Work).
- **Doctor's default checks are strictly read-only.** Config-file inspection
  (parsing known vendor config paths) is the default and only path for
  registration detection; Coral never runs a vendor's `mcp list` (or
  equivalent) command unless the user passes an explicit `--live` flag, which
  prints a warning before running it. This matters concretely for Gemini CLI:
  listing its registrations causes it to initialize configured MCP servers,
  including stdio transports, for a trusted project — i.e. it can execute
  arbitrary commands from vendor configuration, not just report on them.
  Doctor separately reports config/colony reachability, proxy protocol
  health, command dispatch, client installation, and registration scope as
  independent signals (see Component Changes). It does not infer vendor
  authentication from MCP registration and never writes vendor configuration,
  live or not.
- **Gemini CLI only; Antigravity explicitly out of scope.** Google's
  Antigravity CLI is a distinct product from Gemini CLI with its own MCP
  integration model that had not stabilized publicly at RFD-authoring time
  (Aug 2026). This RFD covers Gemini CLI's still-current API-key/enterprise
  MCP path only. Antigravity is tracked as Future Work, not folded into the
  `gemini` adapter — the two CLIs have different binaries, flags, and (likely)
  config schemas, so conflating them would misreport registration state for
  whichever one the user isn't running.

**Benefits:**

- Users already running Claude Code, Codex, or Gemini CLI get a
  copy-pasteable (or directly runnable) native command instead of hand-edited
  JSON/TOML.
- `coral mcp doctor` turns "is this even working?" into a single command
  with a clear breakdown, instead of trial-and-error inside the assistant.
- `coral terminal` stops implying embedded chat is the only way to talk to
  Coral through an AI assistant, without changing its default behavior for
  existing users.

**Architecture Overview:**

```
coral mcp configure --client <codex|claude|gemini|claude-desktop> [--all-colonies]
        │
        ▼
  client adapter registry (detection + native snippet/command per client, scope-aware)
        │
        ▼
  printed native registration command(s), one per colony, deterministically named
        │
        ▼
  user runs it in their vendor CLI ──► vendor CLI spawns `coral mcp proxy` (stdio, RFD 004/100)

coral mcp doctor [--project <dir>] [--colony <id>] [--client <name>] [--live]
        │
        ├─ Coral MCP proxy (once per run, colony-scoped, shown for every client regardless
        │  of that client's registration state — three independent signals, none inferred
        │  from the others):
        │    ├─ config/colony reachability (direct: resolve colony ID, load colony config,
        │    │                              check MCP not disabled, confirm colony process
        │    │                              reachable — no subprocess spawned)
        │    ├─ proxy protocol             (spawn `coral mcp proxy`, MCP `initialize`
        │    │                              round trip over stdio; if this fails for the same
        │    │                              reason config/colony reachability did, the report
        │    │                              says so instead of presenting it as a distinct
        │    │                              protocol bug)
        │    └─ command dispatch           (`tools/call("coral_cli", ["colony","status","--colony","<id>"])`
        │                                   — proves the proxy's real subprocess dispatch works)
        │
        └─ per client (claude / codex / gemini / claude-desktop):
             ├─ installation       (exec.LookPath for native CLIs; "not applicable"
             │                      for Claude Desktop, which has no CLI binary)
             ├─ registration scope (per adapter: local/project/user, rooted at --project;
             │                      DEFAULT is passive config-file inspection only — no vendor
             │                      commands run. `--live` additionally cross-checks via the
             │                      vendor's own `mcp list`, printing a warning first: for
             │                      Gemini this can start configured stdio MCP servers, i.e.
             │                      execute arbitrary vendor-configured commands, not just
             │                      report on them)
             └─ vendor auth        Codex: `codex login status`; Claude/Gemini/Desktop:
                                    unknown (no equivalent check in this RFD)

coral terminal
        │
        ├─ --mode external-assistant [--client <name>] [--project <dir>]
        │                                ──► adapter detection ──► launch
        │                                instructions printed (no process spawned — see
        │                                Component Changes for the 0/1/multi-registration and
        │                                no-TTY contract)
        │                                (branches BEFORE any ask/LLM config is resolved or
        │                                 validated — works with zero ai.ask configuration)
        └─ --mode embedded (unconditional default) ──► embedded LLM setup → TUI (unchanged)
```

### Client Adapter Registration Contract

Registration detection is config-file inspection by default (see Key Design
Decisions). Each adapter defines the following explicitly; exact field names
marked "(Phase 1)" are cross-checked against current vendor docs/behavior
during implementation, matching how this RFD already treats native command
syntax:

| Adapter | Platforms | Config path(s) by scope | How scope is distinguished | Coral-registration criteria | Precedence on same name in multiple scopes | Disabled/rejected/malformed entries | `--project` effect |
|---|---|---|---|---|---|---|---|
| `claude` (Claude Code CLI) | macOS, Linux, Windows | local & user: `~/.claude.json` (single shared file; local and user entries live in different keys within it, confirmed Phase 1); project: `<project>/.mcp.json` | File + in-file key for local/user; a separate file for project | Server entry name equals `coral` or `coral-<id>` AND its `command`/`args` match either the canonical `coral mcp proxy [--colony <id>]` invocation or the legacy `coral colony mcp proxy [--colony <id>]` invocation — name alone is not sufficient (a user could point a same-named entry elsewhere) | local > project > user (most specific to the working directory wins) | An entry present but marked disabled/rejected in the vendor schema (Phase 1: exact field name) is reported as "registered but disabled," never counted as "found" for launch purposes; a config file that fails to parse is reported as a detection *error*, not "not found" | Roots the `.mcp.json` (project) and effective-local-scope lookup in `~/.claude.json` at `--project`; entries outside that tree are not reported |
| `codex` (Codex CLI) | macOS, Linux | project: `.codex/config.toml` files from the trusted project root down to `<project>`; user: `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`); system: `/etc/codex/config.toml` | Separate configuration layers; project layers are loaded only for a trusted project | `[mcp_servers.<name>]` entry name + `command`/`args` match, as above | closest project layer > parent project layers > user > system | `enabled = false` is reported as "registered but disabled" and is not launch-eligible; an untrusted project's `.codex` layers are reported as "registered, untrusted" and ignored for effective resolution; malformed TOML is a detection error | Sets the effective project/cwd used to discover trusted `.codex/config.toml` layers and evaluate precedence; `codex mcp add` still writes the user-level registration because that CLI has no add-time `--scope` flag |
| `gemini` (Gemini CLI) | macOS, Linux, Windows | project: `<project>/.gemini/settings.json`; user: the `.gemini/settings.json` under `GEMINI_CLI_HOME` (default `~/.gemini/settings.json`); system defaults: `/etc/gemini-cli/system-defaults.json` (Linux), `/Library/Application Support/GeminiCli/system-defaults.json` (macOS), or `%ProgramData%\gemini-cli\system-defaults.json` (Windows); system override: the corresponding `settings.json` path; honor `GEMINI_CLI_SYSTEM_DEFAULTS_PATH` and `GEMINI_CLI_SYSTEM_SETTINGS_PATH` | Separate configuration layers | `mcpServers` entry name + `command`/`args` + stdio transport (explicit or defaulted) match, as above | system override > project > user > system defaults | A project entry ignored because the project is untrusted is reported as "registered, untrusted" and is not launch-eligible; malformed JSON is a detection error. Passive trust inspection reads the vendor-home `trustedFolders.json` (or `GEMINI_CLI_TRUSTED_FOLDERS_PATH`) and the user setting that enables folder trust; if an IDE-only trust signal could change the result, report trust as "unknown" rather than "found" | Roots project settings and trust lookup at `--project` |
| `claude-desktop` | macOS, Windows only (no official Linux desktop app); no CLI, no `mcp list` equivalent | Single global file, no scope: `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows) | N/A — one implicit global scope | `mcpServers` entry name + `command`/`args` match, as above | N/A (single scope) | Malformed JSON reported as a detection error | No effect (global config only); doctor reports CLI installation as "not applicable" and still reports config registration, while `coral terminal --mode external-assistant` excludes this adapter because there is no CLI launch command |

`--live` is never available for `claude-desktop` (no vendor command exists to
run). Where this table says a field name is confirmed in Phase 1, the
detection *logic* (name-and-command matching, precedence, disabled handling)
is fixed by this RFD regardless of what Phase 1 finds — only the literal
field/path names are subject to that verification.

Registration results are also colony-aware. A fixed registration named
`coral-<id>` is relevant only when its proxy arguments contain the same
`--colony <id>` and that ID equals doctor's or terminal's resolved colony. A
single-colony `coral` entry without `--colony` is relevant only when running
the proxy from `--project` would resolve the same colony. Registrations for
other colonies remain visible as informational results but are not
launch-eligible. Generated shell commands quote every dynamic token using the
target platform's shell rules; implementations must not interpolate an
unvalidated colony ID or path into copy-pasteable output. Phase 1 adds a shared
server-name/colony-ID validator for the vendor name character sets and a
platform-aware command renderer (POSIX shell and PowerShell).
Each adapter honors the vendor's documented config-root/path environment
overrides rather than assuming the invoking user's default home layout.

### Component Changes

1. **Assistant integration**:

    - Defines a `ClientAdapter` interface: name, installation probe (binary
      names for native CLIs; not applicable for Claude Desktop), known
      config paths/layers (local/project/user/system as supported, rooted at a caller-supplied
      working directory), a function producing the native registration
      command(s) for a given set of colony IDs and an explicit `--scope`, and
      a function to check whether Coral is registered. That function is
      config-file inspection only by default, per the Client Adapter
      Registration Contract above — it never runs a vendor command. A
      separate, explicit "live" check (used only when the caller passes
      `--live`) additionally runs the vendor's own listing command
      (`claude mcp list`, `codex mcp list`, `gemini mcp list`) and merges its
      result with the config-file result rather than replacing it, since the
      config file is the only source that can distinguish scope. Both paths
      return which scope(s) a registration was found in, not a bare bool, and
      the disabled/rejected/malformed handling defined in the contract table.
    - Ships adapters for `claude` (Claude Code CLI — native `claude mcp add
      --scope <scope>`), `codex` (Codex CLI — native `codex mcp add`), and
      `gemini` (Gemini CLI — native `gemini mcp add --scope <scope>
      --transport stdio <name> <command> [args...]`; for multi-colony output,
      place `--` immediately before the proxied `--colony` argument so
      Gemini's parser does not consume it. The required `<command>` positional
      must remain before `--`. The adapter and all its output are labeled "API
      key / enterprise only," never "subscription," per Google's 2026-06-18
      change). Exact native command syntax is verified against each vendor's
      current CLI during implementation (Phase 1).
    - Existing Claude Desktop JSON generation moves here as the
      `claude-desktop` adapter — the only one left emitting a raw config
      snippet, since Claude Desktop has no CLI. Its JSON shape and default
      selection remain unchanged; its printed macOS destination is corrected.

2. **MCP configuration generator**:

    - Adds `--client codex|claude|gemini|claude-desktop` (default:
      `claude-desktop`, preserving current behavior when omitted) and
      `--scope local|project|user` (default: `user`). Scope validation is
      adapter-specific: `claude` accepts all three values; `gemini` accepts
      only `project|user`; `codex`/`claude-desktop` ignore the default but
      reject an explicitly supplied scope with a clear error because they have
      no corresponding vendor scope.
    - Delegates to the matching adapter. The
      `claude-desktop` adapter keeps today's behavior: one JSON snippet
      containing all colonies. The command-based adapters (`claude`, `codex`,
      `gemini`) each emit one command per colony, using the same
      deterministic naming as today's JSON output (`coral` for a single
      colony, `coral-<id>` for multiple). `claude` and `gemini` pass their
      validated `--scope` explicitly; Codex has no corresponding scope.
      These commands are **not** claimed to be idempotent: Claude Code's
      documented behavior is to fail if a server with the same name already
      exists, and Codex/Gemini's re-run behavior is untested. Output copy
      does not promise how the vendor CLI handles re-running a command for an
      existing name; it points the user at that CLI's own `get`/`remove`/`add`
      help instead of asserting "replaces" or "fails."

3. **`coral mcp doctor`**:

    - Runs Coral's own health checks once per invocation, scoped to a colony,
      independent of any client — and shows them identically no matter which
      clients are installed or registered. Three checks, not one, each an
      independent signal:
      - **config/colony reachability** — resolves the colony ID, loads its
        config, confirms MCP is not disabled, and confirms the colony
        process is reachable, all directly (no subprocess spawned). This is
        the same sequence `coral mcp proxy` itself runs before it
        starts serving stdio, extracted so a failure here is reported as
        config/colony state, not misattributed to the proxy or the MCP
        protocol.
      - **proxy protocol** — spawns `coral mcp proxy` and completes an
        MCP `initialize` round trip over stdio. Because the proxy subprocess
        re-runs the same config/colony checks internally before it reads
        stdin, a failure here that matches the config/colony reachability
        check's failure is reported as "config/colony reachability failed
        (see above)," not as a separate protocol defect; a failure here when
        config/colony reachability *passed* is reported as an actual
        protocol-level problem.
      - **command dispatch** — additionally calls `tools/call("coral_cli",
        ["colony", "status", "--colony", "<id>"])` against the same proxy,
        which proves the proxy's real subprocess dispatch works: it spawns an
        actual `coral colony status --colony <id> --format json` subprocess,
        so this is a functional check, not a protocol-only one.

      Each check has its own bounded timeout (proxy protocol: 5s;
      command dispatch: 10s, since it spawns a further subprocess), and
      doctor always closes/cancels and reaps every subprocess tree it spawned,
      using an owned process group on POSIX and the corresponding Windows job
      mechanism, followed by the platform-appropriate kill operation after a
      2s grace period when graceful shutdown fails — no orphaned proxy or
      vendor-started MCP processes survive a doctor run. This is new work:
      `coral mcp proxy` has
      no separate `call-tool` subcommand today (the closest existing command,
      `mcp test-tool`, goes through the colony RPC `CallTool`, a different
      path entirely).
    - Per client, reports installation state and registration scope found
      (never "skipped" because Coral's proxy is unrelated to whether the
      client itself is registered) — modeled loosely on the status-enum
      reporting in `coral ask list-providers`, but every signal kept separate.
      Registration is config-file-only by default (see Client Adapter
      Registration Contract); `--live` additionally runs each installed
      client's listing command, prefixed with a warning that Gemini's live
      check is not read-only, and itself bounded to a 5s timeout per vendor
      call. `mcp doctor` is the only command exposing `--live` in this
      RFD.
    - Accepts `--project <dir>` (default: cwd) to root scope-aware
      registration checks and colony resolution, `--colony <id>` (default:
      the colony resolved from `--project`) to scope all relevance and health
      checks, `--client <name>` to target one adapter, and `--live` (default:
      off) to opt into vendor listing commands. Without `--client`, all
      adapters are reported but unselected alternatives are informational.
    - Reports a fourth, per-client "vendor auth" signal. Codex populates it
      with the documented, non-interactive `codex login status` command;
      Claude Code and Gemini CLI report "unknown" because this RFD does not
      have an equivalent documented, non-interactive command for them.
      Registration-listing commands are never treated as vendor-auth checks.
    - **Exit codes** (shared with `coral terminal --mode external-assistant`,
      see item 4): `0` when every Coral-side health check passes and at least
      one launch-eligible registration relevant to the resolved colony exists;
      when `--client` is supplied, that selected client must instead be
      installed (or installation is not applicable), relevantly registered,
      and authenticated where a supported auth check returns a definite
      result. An `unknown` auth signal is displayed but does not itself fail.
      Other unselected clients are informational and cannot make an otherwise
      healthy run fail. Exit `1` means a health check failed, no usable client
      exists, or a valid selected client is unavailable/unregistered; `2`
      means usage or an internal error (invalid client value, invalid flag
      combination, unreadable required state, or colony resolution failure).
      Implementation adds a typed CLI exit error and root-level mapping because
      today's root maps every returned error to exit `1`.
    - **Output format**: human-readable text only in this RFD's scope; a
      machine-readable `--format json` is explicitly deferred (see Future
      Work) rather than half-specified here.

4. **`coral terminal`**:

    - Adds `--mode embedded|external-assistant|select` (default: `embedded`,
      the unconditional default — no automatic picker on a bare
      `coral terminal` invocation, even in a TTY, so no existing interactive
      user's behavior changes). `--mode select` shows the interactive picker
      explicitly, and works with zero `ai.ask` configuration — if the user
      picks "Embedded chat" from that picker with none configured, it fails
      the same way an unconfigured `coral terminal --mode embedded` already
      does today; this RFD adds no new validation there.
    - Adds `--client <name>` (one of `claude`/`codex`/`gemini`; no default),
      usable only with `--mode external-assistant`, to resolve which
      registered client to use non-interactively. Adds `--project <dir>`
      (default: cwd), valid with `external-assistant` and `select`, to root
      registration detection, colony resolution, and printed launch
      instructions; it has no effect if `select` ultimately chooses embedded
      mode.
    - The mode branch runs before embedded LLM configuration or agent setup.
      `external-assistant` returns via its own path and works with no `ai.ask`
      provider configured.
    - `--mode select` always presents the first-level mode picker (Embedded
      chat versus External assistant). If External assistant is selected, the
      client-selection rules below run as a separate second step; the phrase
      "no picker" below refers only to that second, client picker. Because it
      is explicitly interactive, `--mode select` without a TTY is a usage
      error and exits `2`.
    - `external-assistant` mode lists adapters with a confirmed,
      launch-eligible registration for the resolved colony
      (reusing adapter detection from item 1/3, config-file only — `--live`
      is not offered on `coral terminal` at all, consistent with it never
      running vendor commands anywhere in this RFD), excluding
      `claude-desktop` (it has no CLI process to print launch instructions
      for). Its behavior is fully specified by registration count:
      - If `--client` is supplied, validate it before counting registrations.
        An unsupported value is a usage error (exit `2`); a supported but
        unavailable, unregistered, disabled, untrusted, or wrong-colony client
        is a normal unavailable result (exit `1`). A usable explicit client is
        selected regardless of how many other registrations exist.
      - **Zero** registered clients found and no explicit client: prints guidance pointing at
        `coral mcp configure --client <name>` for each
        supported adapter and exits `1` — it does not silently fall back to
        embedded mode.
      - **Exactly one** registered client found and no explicit client: prints
        that client's launch instructions directly, with or without a TTY,
        and exits `0`. No second-level client picker is shown.
      - **More than one** registered client found and no explicit client: if
        stdin is a TTY, shows an interactive client picker; otherwise prints
        the registered names with instructions to re-run with `--client
        <name>` and exits `1`.
    - **Launch instructions** (this RFD prints instructions; it does not
      spawn or exec anything — direct process handoff is Future Work): the
      bare vendor CLI invocation to start an interactive session (e.g.
      `claude`, `codex`, or `gemini`, no arguments), labeled with which
      effective registration scope was found and which colony terminal resolved,
      and a note that the vendor CLI must be run from `--project`'s directory
      (default: cwd) because project-scope registrations resolve relative to
      the vendor's own working directory, which Coral does not change on the
      user's behalf.

5. **Coral MCP proxy protocol compliance** (`coral mcp proxy`):

    - The proxy's request loop currently treats every unrecognized method,
      including id-less JSON-RPC *notifications* like `notifications/initialized`
      (which every adapter's vendor CLI sends after a successful `initialize`
      handshake), as an RPC call needing an error response — it replies
      `-32601 method not found` instead of silently accepting the
      notification, which is what JSON-RPC 2.0 and the MCP spec require for
      messages with no `id`. This RFD fixes that as a prerequisite for the
      doctor proxy-protocol check to be meaningful (see Phase 3): the proxy
      must recognize `notifications/initialized` and any other id-less
      request as a notification and send no response, rather than only
      distinguishing behavior by method name.

**Configuration Example:**

No new persisted configuration. `coral terminal --mode` is a per-invocation
flag, not a stored preference, in this RFD's scope.

## Implementation Plan

### Phase 1: Client Adapter Foundation

- [ ] Create an assistant integration component with a `ClientAdapter`
      interface (accepts a working directory; reports registration by scope,
      not bool; config-file inspection is the only registration-check path
      wired by default — see below) and a registry
- [ ] Verify current native MCP-registration syntax against each vendor's
      docs: Claude Code (`claude mcp add --scope`, `claude mcp list`), Codex
      (`codex mcp add`, `codex mcp list`), Gemini CLI (`gemini mcp add
      --scope --transport stdio <name> <command> [args...]`, confirming that
      `<command>` precedes `--` and that `--` is inserted immediately before
      a proxied flag such as `--colony`; `gemini mcp list`);
      implement all three adapters using native commands, not hand-authored
      config, with `--scope` passed explicitly (default `user`) where the
      vendor supports a scope
- [ ] Implement config-file registration detection per the Client Adapter
      Registration Contract table: exact config path(s) per adapter/scope,
      name-and-command matching (not name alone), scope precedence, and
      disabled/rejected/malformed handling; include Codex's trusted project,
      user, and system layers and Gemini's project, user, system-default, and
      system-override layers; honor documented vendor config-root/path
      environment overrides; confirm Claude's local-vs-user key split within
      `~/.claude.json` against current vendor behavior
- [ ] Implement passive Gemini trust evaluation from the user folder-trust
      setting and `~/.gemini/trustedFolders.json` (honoring
      `GEMINI_CLI_TRUSTED_FOLDERS_PATH`); report "unknown" rather than
      launch-eligible if an IDE-only trust signal prevents a conclusive result
- [ ] Implement colony-aware relevance matching and the shared safe command
      renderer: validate vendor server names/colony IDs and quote dynamic
      tokens for POSIX shell or PowerShell; unit-test spaces, metacharacters,
      fixed `--colony` registrations, and wrong-colony registrations
- [ ] Implement the separate `--live` detection path per adapter (invokes
      `mcp list` or equivalent, merged with — not replacing — the config-file
      result), gated so it only ever runs when the caller explicitly opts in;
      exposed only through `mcp doctor --live`; `configure` and
      `coral terminal` never invoke it
- [ ] Label every `gemini` adapter surface "API key / enterprise only," never
      "subscription," per Google's 2026-06-18 change, and do not add or infer
      support for Google's separate Antigravity CLI (see Key Design
      Decisions)
- [ ] Implement Codex vendor-auth detection with `codex login status`, bounded
      and non-interactive; Claude Code and Gemini CLI remain "unknown" unless
      Phase 1 confirms an equivalent documented, non-interactive command.
      Never infer vendor auth from an MCP registration-listing command
- [ ] Port existing Claude Desktop JSON generation into a `claude-desktop`
      adapter with unchanged JSON shape, but correct the printed macOS config
      destination to `~/Library/Application Support/Claude/claude_desktop_config.json`;
      report its registration (with installation "not applicable") in doctor,
      and exclude it from `--live` and `coral terminal`'s external-assistant
      client list because no CLI command exists
- [ ] Unit tests: each adapter's generated command for single- and
      multi-colony input at each supported `--scope` (including the Gemini
      positional/`--` placement), rejection of unsupported client/scope combinations,
      config-file-only registration detection against fixture config trees
      (including disabled/malformed fixtures), and `--live` detection against
      mocked `mcp list` output, kept in separate test cases from the
      config-file path

### Phase 2: `--client` Flag on `coral mcp configure`

- [ ] Add `--client` (default `claude-desktop`) and `--scope`
      (default `user`, applied to `claude`/`gemini` only)
- [ ] Route output through the matching adapter; for command-based adapters
      (`claude`, `codex`, `gemini`), emit one command per colony with
      deterministic naming (`coral` / `coral-<id>`, matching today's JSON
      naming); output copy points at the vendor's own `get`/`remove`/`add`
      help for re-registration instead of asserting untested "replaces"
      behavior
- [ ] If `--all-colonies` resolves zero configured colonies, return a clear
      error instead of printing an empty registration block
- [ ] Update help text (remove the "Claude Desktop format only" note)
- [ ] Unit tests covering all four `--client` values and every supported
      client/scope combination, single and `--all-colonies`, including
      multi-colony output for each command-based adapter

### Phase 3: `coral mcp doctor`

- [ ] **Prerequisite:** repair the currently-skipped `TestMCPProxyE2E` (it
      `t.Skip`s unconditionally today and still asserts the retired
      multi-tool MCP model — a two-tool `tools/list` response — instead of
      the current single `coral_cli` meta-tool from RFD 100); doctor's proxy
      checks build directly on this same subprocess-and-stdio harness, so a
      broken foundation would make the new checks untestable
- [ ] Fix the proxy's notification handling (see Component Changes #5):
      `notifications/initialized` and any other id-less request must get no
      response, instead of the current `-32601 method not found` reply;
      cover with a test in the repaired E2E suite
- [ ] Add `coral mcp doctor`, plus a hidden or deprecated `coral assistant
      doctor` compatibility alias, with `--project <dir>`
      (default: cwd), `--colony <id>` (default: the colony resolved from
      `--project`), `--client <claude|codex|gemini|claude-desktop>` (optional
      target), and `--live` (default: off)
- [ ] Implement the config/colony reachability check directly (resolve
      colony ID, load colony config, confirm MCP not disabled, confirm colony
      reachable) without spawning a subprocess — this is the same sequence
      `coral mcp proxy` runs internally, extracted so its failure
      mode is attributable
- [ ] Implement the proxy-protocol check: spawn `coral mcp proxy`
      directly and complete an MCP `initialize` round trip over stdio, with a
      5s timeout; when it fails, compare the failure against the
      config/colony reachability check's result and report a shared root
      cause instead of a separate protocol defect when they match — new
      capability, not a reuse of an existing subcommand (`mcp test-tool` goes
      through the colony RPC `CallTool`, a different path)
- [ ] Implement the command-dispatch check as a separate step with a 10s
      timeout: `tools/call("coral_cli", ["colony", "status", "--colony", "<id>"])`
      against the same spawned proxy, verifying it actually shells out and
      returns successfully — run once per invocation, never gated on any
      client's registration state
- [ ] Guarantee cleanup: every subprocess doctor spawns (proxy checks,
      auth checks, and `--live` vendor calls) first receives graceful stdin
      close/cancellation, then its owned process tree is killed after a 2s
      grace period using a POSIX process group or Windows job object; do not
      kill only the vendor CLI parent and orphan an MCP server it started.
      Reap every process on success and failure paths
- [ ] Implement installation detection (`exec.LookPath` for native CLIs;
      "not applicable" for Claude Desktop) and
      config-file-only registration detection by default, per the Client
      Adapter Registration Contract (scope-aware, rooted at `--project`);
      wire `--live` to additionally run each installed native client's listing
      command (`claude mcp list`, `codex mcp list`, `gemini mcp list` if it
      exists), printing the non-read-only warning before running it, bounded
      to 5s per vendor call; reject `--client claude-desktop --live` as an
      invalid flag combination
- [ ] Populate Codex vendor auth with bounded `codex login status`; report
      "unknown" for Claude Code, Gemini CLI, and Claude Desktop
- [ ] Add a typed CLI exit error and update `cmd/coral`'s root error mapping so
      commands can return `1` for an unhealthy/unavailable result and `2` for
      usage/internal errors without calling `os.Exit` inside command packages;
      implement doctor's aggregation contract from Component Changes #3
- [ ] Human-readable report only in this phase (`--format json` deferred to
      Future Work): config/colony reachability, proxy protocol, and command
      dispatch shown as three separate lines; per-client signals kept
      separate underneath; unit tests with fixture binaries/configs/mocked
      `mcp list` output for each detection state, plus exit-code assertions
      for each outcome

### Phase 4: `coral terminal` External Assistant Mode

- [ ] Add `--mode embedded|external-assistant|select` flag; default
      `embedded` stays the **unconditional** default (no automatic picker,
      even in a TTY) — `select` is the only way to reach the interactive
      picker
- [ ] Add `--client <name>` flag, valid only with `--mode external-assistant`,
      for non-interactive/no-TTY resolution among multiple registrations
- [ ] Add `--project <dir>` (default: cwd), valid with
      `--mode external-assistant|select`, to root colony resolution,
      registration detection, and launch instructions
- [ ] Branch external-assistant mode before embedded LLM configuration and
      agent setup, so it never touches `ask`/LLM config
- [ ] Picker copy (shown only under `--mode select`) distinguishes "External
      assistant (hosts Coral MCP; auth handled by that client — subscription,
      API key, or enterprise depending on which one)" from "Embedded chat
      (needs an API key, enterprise gateway, or local model)"
- [ ] Keep the `select` mode picker separate from the external client picker,
      then implement the full external-assistant interaction contract from
      Component Changes #4: config-file-only adapter detection excluding
      `claude-desktop`; colony-aware relevance filtering; explicit-client
      validation before registration counting; zero-registration guidance +
      exit `1`; single-registration auto-print + exit `0`; and
      multi-registration branching on TTY presence, including the
      no-TTY-no-`--client` exit `1`, unsupported-client exit `2`, and
      supported-but-unavailable-client exit `1` cases
- [ ] Reject `--mode select` without a TTY as a usage error (exit `2`)
- [ ] Implement launch-instruction output: bare vendor CLI invocation, found
      scope, resolved colony, and the note to run it from `--project`'s
      directory — printing instructions only, no process spawned (direct
      handoff is Future Work)
- [ ] Test: `coral terminal --mode external-assistant` succeeds with no
      `ai.ask` credentials or provider configured at all
- [ ] Unit/integration tests for mode flag parsing, the unconditional default,
      picker copy, the full zero/one/multi × TTY/no-TTY × `--client` matrix,
      and exit codes for each branch

### Phase 5: Documentation

- [ ] Update MCP documentation: per-client `--client` registration instructions,
      `coral mcp doctor`, and the plain-language distinction between
      external, vendor-hosted assistants (Claude Code, Codex — subscription or
      API key; Gemini CLI — API key/enterprise only, per Google's 2026-06-18
      change) and embedded chat
- [ ] Update CLI documentation: `--client`/`--scope` on `configure`,
      `--client`/`--live` and exit codes on `doctor`, `--client`/`--project`
      on `coral terminal`, and the config-file-default/`--live` distinction
- [ ] Update the CLI-to-MCP mapping: note `mcp doctor` is a local
      diagnostic, not an MCP-exposed tool
- [ ] Update provider documentation: clarify that external assistant CLIs
      (Claude Code, Codex, Gemini CLI) are MCP clients, not embedded LLM
      providers, and why — including that Gemini CLI's auth model there is
      API key/enterprise only, not a personal subscription, and that
      Antigravity is explicitly out of scope (see Key Design Decisions)
- [ ] Update installation documentation: add the external-assistant onboarding
      path (`mcp configure --client …` → `mcp doctor`) alongside the
      existing API-key path, noting per-client auth requirements

## API Changes

### CLI Commands

```bash
# Native registration for Claude Code CLI (single colony, default --scope user)
coral mcp configure --client claude

# Example output:
Claude Code CLI Registration (scope: user)
Run this command to register Coral MCP with Claude Code:

  claude mcp add coral --scope user -- coral mcp proxy

To re-register under a different scope, or if "coral" already exists, see:
`claude mcp add --help`, `claude mcp get coral`, `claude mcp remove coral`.
Check it took with: claude mcp list

# Native registration for Claude Code CLI (multi-colony) — one command per
# colony, same coral / coral-<id> naming as the Claude Desktop JSON output.
# Not claimed idempotent: re-running against an existing name is Claude
# Code's documented behavior to fail, not Coral's to promise.
coral mcp configure --client claude --all-colonies --scope project

# Example output:
Claude Code CLI Registration (2 colonies, scope: project)
Run these commands to register each colony as a separate MCP server:

  claude mcp add coral-shop-prod --scope project -- coral mcp proxy --colony shop-prod
  claude mcp add coral-shop-staging --scope project -- coral mcp proxy --colony shop-staging

If a name already exists, see `claude mcp add --help` for how Claude Code
handles it — Coral does not manage collisions itself.
Check it took with: claude mcp list

# Native registration for Codex CLI
coral mcp configure --client codex

# Example output:
Codex CLI Registration
Run this command to register Coral MCP with Codex:

  codex mcp add coral -- coral mcp proxy

If "coral" already exists, see `codex mcp add --help` for Codex's behavior.
Check it took with: codex mcp list

# Native registration for Gemini CLI — API key / enterprise auth only. Gemini
# CLI stopped serving individual, free, and Google AI Pro/Ultra accounts on
# 2026-06-18: https://github.com/google-gemini/gemini-cli/discussions/28017
coral mcp configure --client gemini

# Example output:
Gemini CLI Registration (scope: user, API key / enterprise accounts only)
Run this command to register Coral MCP with Gemini CLI:

  gemini mcp add --scope user --transport stdio coral coral mcp proxy

The single-colony command needs no separator because none of the server
arguments begin with a flag. This only works if Gemini CLI is configured with
an API key or enterprise credentials — it no longer supports
individual/free/Google AI Pro/Ultra accounts, and does not cover Google's
separate Antigravity CLI. If "coral" already exists, see `gemini mcp add
--help`.

# Native registration for Gemini CLI (multi-colony) — "--" appears immediately
# before the proxied "--colony" flag, after Gemini's required command positional
coral mcp configure --client gemini --all-colonies --scope project

# Example output:
Gemini CLI Registration (2 colonies, scope: project, API key / enterprise accounts only)
Run these commands to register each colony as a separate MCP server:

  gemini mcp add --scope project --transport stdio coral-shop-prod coral mcp proxy -- --colony shop-prod
  gemini mcp add --scope project --transport stdio coral-shop-staging coral mcp proxy -- --colony shop-staging

# Default / explicit Claude Desktop adapter — Claude Desktop has no CLI, so
# this remains the one config-snippet adapter
coral mcp configure
coral mcp configure --client claude-desktop
```

```bash
# Diagnose Coral's own health, plus installed clients and their registration.
# Default registration detection is passive: no vendor `mcp list` commands run.
# If Codex is installed, its documented read-only `codex login status` auth
# check still runs with a bounded timeout.
coral mcp doctor
coral mcp doctor --project /path/to/app --colony shop-prod   # defaults: cwd, colony resolved from --project
coral mcp doctor --client codex                              # target one client

# Example output (exit 0: Coral is healthy and one relevant client is usable):
Coral Assistant Doctor  (project: /Users/alex/app, colony: shop-prod)
=====================================================================

Coral MCP proxy
  config/colony reachability   [healthy]   colony "shop-prod" reachable, MCP enabled (4ms)
  proxy protocol                [healthy]   coral mcp proxy: initialize ok (18ms)
  command dispatch              [healthy]   tools/call("coral_cli", ["colony","status","--colony","shop-prod"]) ok (61ms)

Claude Code CLI         [installed]        binary: /usr/local/bin/claude
  registration           [found: project]   coral -> coral mcp proxy (via <project>/.mcp.json)
  vendor auth             [unknown]          no documented non-interactive auth-status command

Codex CLI                [not found]
  Install: https://developers.openai.com/codex/cli

Gemini CLI               [installed]        binary: /usr/local/bin/gemini  (API key / enterprise only)
  registration            [not found]        checked: project, user, system-default, system-override layers
                                              run: coral mcp configure --client gemini
  vendor auth              [unknown]          no documented non-interactive auth-status command

Claude Desktop            [installation: n/a] no CLI binary
  registration             [not found]        checked: ~/Library/Application Support/Claude/claude_desktop_config.json

Coral MCP proxy: healthy. Native CLIs: 2/3 installed. Relevant registrations: 1/4 adapters.
Exit code: 0 (Coral is healthy and Claude Code is launch-eligible;
              unselected Codex, Gemini, and Claude Desktop results are informational)

# --live additionally cross-checks via each vendor's own listing command.
# Warns first because it is NOT read-only for every client: Gemini CLI
# initializes configured stdio MCP servers — i.e. runs vendor-configured
# commands — when listing registrations in a trusted project.
coral mcp doctor --live

# Example additional output with --live:
Warning: --live runs each installed client's own registration-listing
command. For Gemini CLI this can start configured MCP servers (arbitrary
commands from its config), not just report on them. Proceeding...

  registration           [found: project]   confirmed live via `claude mcp list` (312ms)
```

```bash
# coral terminal: explicit mode selection — embedded is the unconditional
# default, unchanged for every existing interactive user
coral terminal                              # embedded chat (default, unchanged)
coral terminal --mode embedded              # same as above, explicit
coral terminal --mode external-assistant    # skip ask/LLM setup; print launch instructions for a registered client
coral terminal --mode external-assistant --client codex   # skip the picker when multiple clients are registered
coral terminal --mode external-assistant --project /path/to/app
coral terminal --mode select --project /path/to/app        # mode picker first; client picker only if needed

# Example output, exactly one client registered (exit 0, no picker shown):
External assistant: Claude Code CLI (registration: project scope, colony: shop-prod)
Run this in your shell, from /Users/alex/app, to start it:

  claude

Coral MCP tools will be available once Claude Code connects.

# Example output, zero clients registered (exit 1):
No external assistant is registered for this project.
Register one first, e.g.:
  coral mcp configure --client claude
  coral mcp configure --client codex
  coral mcp configure --client gemini

# Example output, multiple clients registered, no TTY and no --client (exit 1):
Multiple external assistants are registered: claude, gemini.
Re-run with --client <name> to choose one non-interactively.
```

## Testing Strategy

### Unit Tests

- Each client adapter: correct command for single- and
  multi-colony input at each `--scope` (including Gemini positional and `--`
  placement),
  including command-based multi-colony naming
- `configure --client`/`--scope` flag routing for all supported values
  and a clear error for empty `--all-colonies`
- Config-file-only registration detection against fixture config trees for
  each adapter/scope in the Client Adapter Registration Contract table,
  including disabled/rejected and malformed-file fixtures (malformed must
  report as an error, not "not found")
- `--live` registration detection against mocked `mcp list` output, as
  separate test cases from the config-file path, asserting it is never
  invoked unless `--live` is passed
- `mcp doctor` exit-code assertions for each of the three outcomes
  (`0`/`1`/`2`) across representative healthy/degraded/usage-error scenarios;
  Codex vendor auth exercised through mocked `codex login status`, with
  "unknown" asserted for Claude Code, Gemini CLI, and Claude Desktop; verify
  unselected missing clients do not change an otherwise healthy exit `0`
- `coral terminal --mode`/`--client`/`--project` flag parsing, confirming `embedded`
  remains the unconditional default (no picker), `select` is required to
  reach the mode picker, and the full zero/one/multi-registration ×
  TTY/no-TTY × `--client` branching matrix from Component Changes #4,
  including its exit codes

### Integration Tests

- The config/colony reachability check against a real (and separately, a
  disabled/unreachable) colony, verified independent of the proxy subprocess
- `mcp doctor`'s proxy-protocol check against a real `coral mcp proxy`
  process: spawn it, complete an `initialize` round trip including a
  `notifications/initialized` follow-up that must receive no response, and
  verify it reports "healthy" independent of any client's registration state
- A case where config/colony reachability fails and the proxy-protocol check
  is asserted to report the same root cause rather than an independent
  protocol defect
- `mcp doctor`'s command-dispatch check against the same live proxy:
  `tools/call("coral_cli", ["colony", "status", "--colony", "<id>"])`, verifying it
  actually spawns `coral colony status --colony <id> --format json`, and all
  three checks respect their bounded timeouts (5s/5s/10s) and leave no
  orphaned subprocess behind on timeout
- `coral terminal --mode external-assistant` with no `ai.ask` credentials or
  provider configured anywhere, verifying it bypasses embedded LLM setup and
  still prints launch instructions

### E2E Tests

- Repair `TestMCPProxyE2E` first (currently skipped and asserting the
  retired multi-tool model — see Phase 3) so it exercises the current
  single-`coral_cli`-tool model and the `notifications/initialized` fix
- Automated E2E beyond that is limited to fixture-based detection (real
  vendor CLI installs aren't guaranteed in CI); manual verification against
  actually installed Claude Code, Codex, and Gemini CLIs — including running
  `--live` against each — is required before marking each adapter complete

## Security Considerations

- `coral mcp doctor` registration detection is config-file inspection
  only by default: it reads known client config paths to check for a
  registration entry, never executes file contents, and never writes vendor
  configuration. `configure` only prints registration material and does
  not perform registration detection.
- `--live` is the one deliberate exception to that default and is explicitly
  **not** read-only for every client: Gemini CLI initializes configured MCP
  servers, including stdio transports, when listing registrations in a
  trusted project — meaning it can execute arbitrary commands from vendor
  configuration Coral did not write. `--live` always prints this warning
  before running any vendor command, exists only on doctor, defaults off, and
  is never available for `claude-desktop` (no vendor command exists to run).
- Generated registration output never embeds secrets: `coral mcp proxy`
  carries no credentials in its invocation, consistent with existing
  `configure` behavior. Dynamic server names, colony IDs, executable
  paths, and project paths are validated and rendered with platform-appropriate
  shell quoting; raw config values are never interpolated into a copy-pasteable
  command.
- No new attack surface on the MCP proxy itself — `mcp doctor`'s
  proxy-protocol and command-dispatch checks, and the external-assistant
  launch-instructions path, all either read config or drive the existing
  `coral mcp proxy` path (RFD 004/100) under the invoking user's local
  privileges; nothing runs with elevated privileges as a result of this RFD.
  `coral terminal --mode external-assistant` never spawns the vendor CLI
  itself — it only prints the command for the user to run.
- Under `--live`, registration detection only ever invokes a command an
  adapter explicitly declares as its official listing command (`claude mcp
  list`, `codex mcp list`, `gemini mcp list`); it never triggers a login
  flow, opens a browser, or starts an interactive assistant session on the
  user's behalf — the Gemini stdio-server-startup behavior above is a
  documented side effect of that listing command itself, not something Coral
  adds. Codex's separate `codex login status` auth check is documented and
  non-interactive; it is bounded by the same timeout and never prints stored
  credentials.
- Every subprocess tree `mcp doctor` spawns (proxy checks, Codex auth,
  and `--live` vendor calls) is gracefully closed or cancelled, then
  terminated through an owned POSIX process group or Windows job object if
  necessary, and reaped on success and timeout paths — no vendor-started MCP
  child is left behind by a doctor run.

## Implementation Status

**Core Capability:** 🟡 Partially Implemented

The top-level `coral mcp` command group, canonical `configure` and `proxy`
paths, hidden `coral colony mcp` compatibility group, and canonical proxy path
in generated Claude Desktop configuration are implemented. This RFD will add
`--client`/`--scope` support to `coral mcp configure` for Claude Code, Codex,
and Gemini CLIs (native `mcp add`
commands for all three, `--` argument-separated where needed; Gemini clearly
labeled API-key/enterprise-only, never subscription, and never Antigravity);
effective, colony-aware config-file registration detection by default per adapter
(vendor `mcp list` gated behind `--live`, with a printed non-read-only
warning); a repaired, current-model proxy E2E test and a
`notifications/initialized` protocol-compliance fix as prerequisites; a new
`coral mcp doctor` diagnostic command that reports three independent
Coral-side signals (config/colony reachability, proxy protocol, command
dispatch) once per run, client-independent, plus per-client
installation/registration signals (Codex auth via `codex login status`, other
auth signals unknown) and a shared, root-supported `0`/`1`/`2` exit-code convention;
an explicit "External assistant" mode in `coral terminal` that branches
before any `ask`/LLM setup, fully specifies its zero/one/multi-registration
and TTY/`--client` behavior, and prints launch instructions rather than
spawning anything, alongside the existing embedded BYO-API chat as the
unconditional default; and documentation making the per-client auth
distinction, the config-file/`--live` distinction, and the Gemini/Antigravity
scope decision explicit.

## Future Work

**Auto-fix registration** (Future)
- `coral mcp doctor --fix` to write client config directly, requires a
  confirmation UX that won't clobber a user's existing MCP entries

**Direct process handoff from `coral terminal`** (Future)
- Spawn the external client CLI as an interactive subprocess (PTY passthrough
  or exec replacement) instead of printing launch instructions — this is the
  feature that would make "handoff" literally true; this RFD only prints
  instructions

**Additional MCP clients** (Future)
- Cursor, Windsurf, and other IDE-integrated MCP clients, added as adapters
  once demand is confirmed

**Antigravity CLI adapter** (Future, contingent)
- Deliberately deferred rather than folded into the `gemini` adapter (see Key
  Design Decisions): Google's Antigravity CLI is a distinct product from
  Gemini CLI, and conflating them would misreport registration state.
  Revisit once Antigravity's MCP integration model and CLI surface are
  documented and stable enough to write a real adapter contract against.

**Machine-readable doctor output** (Future)
- `coral mcp doctor --format json`, deferred here to keep this RFD's
  human-readable report format from being half-specified alongside a JSON
  schema; the exit-code convention defined in this RFD (`0`/`1`/`2`) should
  carry over unchanged

**Additional vendor trust/auth signals** (Future)
- Populate Claude Code and Gemini CLI vendor auth if they ship documented,
  non-interactive status commands. Refine Gemini's passive project-trust
  result if a documented non-interactive source becomes available for
  otherwise-indeterminate IDE-provided trust.

**Enterprise gateway provider for embedded chat** (Future, related)
- Embedded chat's API-key/gateway/local-model story already exists (RFD 014,
  RFD 030, RFD 055); a dedicated enterprise-gateway
  provider is out of scope for this RFD and would be tracked separately if
  needed

**Gemini CLI individual/subscription support** (Future, contingent)
- Out of reach today: Gemini CLI serves only API-key/enterprise auth as of
  2026-06-18. If Google reinstates individual-account support, revisit the
  `gemini` adapter's labeling and re-check whether a native `add` subcommand
  has since shipped
