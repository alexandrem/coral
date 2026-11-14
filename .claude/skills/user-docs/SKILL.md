---
name: user-docs
description: Write, revise, or consolidate Coral's user documentation in docs/user/. Use for installation, configuration, operation, troubleshooting, and feature walkthroughs, and for maintaining the single man-style docs/CLI_REFERENCE.md. Internal system explanations belong to architecture-docs.
---

# Coral User Documentation

Maintain a practical guide for people installing and using Coral. Explain the
small amount of system context a user needs, then help them complete a task and
recognize its result. Before drafting substantial prose, read
[references/writing-guidelines.md](references/writing-guidelines.md).

## Canonical homes

- `docs/user/README.md` is the reading map. Chapters own setup, configuration
  choices, workflows, operations, and recovery. Use descriptive kebab-case names;
  express reading order in the map. Each chapter should also work as an entry
  point with linked prerequisites.
- `docs/CLI_REFERENCE.md` is the **single man-style CLI reference**. Keep command
  synopsis, arguments, flags, defaults, aliases, environment, files, and material
  exit behavior there. Do not create parallel per-binary command catalogs.
- `docs/user/configuration-reference.md` and `docs/user/sdk-reference.md` own
  configuration and TypeScript interface reference. Keep explanations of choices
  in the workflow chapters; `docs/user/configuration.md` owns configuration
  workflows and remote workstation setup. Follow the
  [configuration guidance](references/writing-guidelines.md#configuration-files-and-examples).
  Link to maintained deployment manifests and runnable examples instead of
  copying their complete contents into chapters.
- `docs/architecture/` explains internals. `docs/development/` covers contributor
  work. `RFDs/` records proposals and design history; it is not a feature list.

Give each procedure one canonical home. An ordinary feature edit does not
authorize a repository-wide migration.

When consolidating, update inbound and section links (including in design
history), remove superseded guides once their material is accounted for, and do
not recreate deleted navigation stubs. Review a shortened legacy page's original
contents in version control before concluding the new guide covers it. Preserve
useful procedures and examples unless they are obsolete, unsupported, or already
covered at the destination.

## Verify the user contract

Check relevant command registration, handlers, configuration loading, defaults,
deployment files, and meaningful tests before documenting behavior. Existing
guides and RFDs are leads, not evidence that a feature shipped. Check the code
behind help text too: a registered placeholder is not a working operation.

For a walkthrough, establish where commands run, which binary is used, required
privileges and credentials, addresses and ports, configuration and data paths,
and how to verify and stop the operation. Mark placeholders and distinguish a
complete file from a fragment. Keep units and service names consistent. Avoid
asserting uniform flags, output formats, or target selection across commands.

Coral-specific distinctions affect user decisions:

- Local process discovery differs from Colony endpoint Discovery; observed
  services differ from explicitly watched services and their health checks.
- A public WireGuard UDP endpoint, mesh address, bootstrap TCP endpoint, and
  operator HTTPS endpoint serve different purposes.
- A CA fingerprint verifies identity, a Bootstrap PSK authorizes enrollment,
  and an API token authorizes access to the public API. Mesh encryption does
  not establish per-command authorization.
- Linux eBPF, host metrics, runtime heap profiling, SDK metadata, and symbol
  availability have different prerequisites. Do not promise equivalent coverage
  on every platform or binary.
- Sampling, polling, retention, and inferred correlations limit the evidence a
  query can return. Put these limits beside the workflow they affect.

Check loader behavior rather than copying help text for paths: `CORAL_CONFIG`
currently replaces the base directory, to which the loader appends `.coral`.
Project selection reads the current directory's `.coral/config.yaml`; it does
not search parent directories.

## Review

Read the result as a user: can they complete the task, understand the output,
and take a useful next step if it fails? Complete the reader pass at the end of
the writing guidelines. Run `make docs-check` for links, fragments, reading-map
coverage, YAML examples, and shell syntax. Also check
reading-map coverage, fenced examples, and reference coverage against the current
command tree. Run documentation checks for documentation-only work; use code
checks when implementation also changes. Do not run privileged deployments merely
to validate prose. Report any walkthroughs that were checked against source but
not exercised on a running deployment.

For changed configuration examples, check YAML parsing, field names, and duration
values against the appropriate component schema. Check shell syntax where
practical. When regrouping reference tables, account for existing fields and
defaults, and preserve section anchors or update their inbound links.
