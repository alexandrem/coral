---
name: architecture-docs
description: Write, revise, or organize Coral's internal architecture documentation in docs/architecture/. Use for explanations of implemented components, data ownership, flows, invariants, and failure behavior, including consolidation of legacy engineering docs.
---

# Coral Architecture Documentation

Maintain an explanatory technical book about Coral's internal workings. Write
for an engineer learning the system or returning to an unfamiliar subsystem:
explain how an operation works, why its mechanisms matter, and where to look
in the implementation.

Before drafting or substantially rewriting prose, read
[references/writing-guidelines.md](references/writing-guidelines.md). It contains
the complete editorial guidance; its FoundationDB links are attribution, not
sources to fetch during ordinary documentation work.

## Documentation responsibilities

- `docs/architecture/` explains the implemented system: component boundaries,
  identities, data ownership, flows, invariants, and failure behavior.
- `RFDs/` records design proposals, decisions, and implementation plans. Check
  the code before treating an RFD as a description of current behavior.
- `docs/user/` owns installation, configuration choices, walkthroughs,
  deployment, inspection, and recovery procedures. Its `README.md` is the reading
  map; use the `user-docs` skill for this audience. `docs/CLI_REFERENCE.md` is the
  single man-style command manual. `docs/user/sdk-reference.md` and
  `docs/user/configuration-reference.md` own interface and configuration reference.
- `docs/DESIGN.md` records broader design context; it is not evidence that a
  proposed capability is implemented.

Give each architectural explanation one canonical home. Other pages can provide
brief context and link to it. Link to procedures and design history where they
help explain a boundary, rather than duplicating them.

## Book organization

Use `docs/architecture/README.md` as the reading map. Offer a progression from
the system model through connectivity and identity, collection and storage, to
investigation. Also make individual subjects easy to find directly.

Use descriptive kebab-case filenames, such as `data-ownership.md`. Express
reading order in the map, not filename numbers. Group pages around concepts
and mechanisms; split when a subject has its own explanatory arc or audience,
not merely because a page is long. Introduce prerequisites and link to their
canonical explanations so each chapter also works as an independent entry point.
Update navigation when a page is added, moved, or changes scope.

Choose headings for the subject instead of imposing a chapter template. A
chapter can develop its model through a representative operation and then
explain guarantees and limitations. Put limitations beside the mechanism they
qualify. Future proposals belong in RFDs; do not require future-notes sections.
Make relevant RFDs and implementation anchors easy to find.

## Repository grounding

Read relevant code, schemas, configuration, and meaningful tests before
describing behavior. Use RFDs for intent and rationale, verifying claims against
the implementation. State material uncertainty or discrepancies explicitly;
do not invent a guarantee or present an approved design as shipped behavior.

For the mechanism at hand, establish authoritative state, writers, readers,
identities, transaction or publication boundaries, and derived data. Explain
concurrency, retries, retention, freshness, deletion, and recovery when they
affect observable behavior. Distinguish configured defaults from deployment
observations, and best-effort inference from guarantees.

Coral has several distinct boundaries that need particular care:

- Process/service discovery within an agent differs from mesh Discovery and
  enrollment. Define which meaning is in use.
- WireGuard reachability, TLS identity, and enrollment authorization are
  separate mechanisms. Check the actual listener and handler before making
  claims about encryption, mTLS, revocation, or access control.
- Sequence checkpoints are scoped by agent and data type, with a storage
  session identifier. Not all telemetry uses the same persistence or transport.
- Service topology and trace/profile correlation infer relationships from
  evidence; explain their matching and attribution limits.

Use relative repository links and stable code symbols, not source line numbers
or machine-specific paths. Include schema fields, API statuses, configuration
names, or constants when they explain a contract; link to exhaustive definitions.

## Editing and consolidation

For an ordinary change, revise the affected explanation and cross-references.
Integrate new behavior into its flow or model instead of appending an RFD recap.

During an authorized consolidation, inspect legacy architecture and engineering
pages, reconcile overlapping accounts against code, and preserve useful
explanations in their canonical destination. Update inbound links; retain a
brief navigation stub when an older entry point still needs to work. Editing
one topic does not itself authorize migrating the entire documentation set.

Check changed local links, navigation, terminology, and any diagrams. Review for
unexplained prerequisites, duplicate accounts, unsupported claims, and roadmap
material presented as current behavior. Documentation-only changes need
documentation checks; run applicable code checks if code also changes.
