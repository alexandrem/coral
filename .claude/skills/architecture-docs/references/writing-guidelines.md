# Architecture Writing Guidelines

Write Coral's architecture as a technical book that develops the reader's
understanding of the system. Use calm, direct, explanatory prose. Assume the
reader knows software engineering but has not memorized Coral's terminology,
history, or package layout.

## Develop the model through an operation

Start with the problem a subsystem solves and a compact account of its model.
Define what a piece of state represents before explaining its storage or
processing. Describe each component through its responsibility and interactions,
then follow an operation from its trigger through state changes to the result
observed by a reader or downstream consumer.

Connect representation to behavior. Explain how an identity, ordering rule,
relationship, or publication boundary determines what a writer can change and
what a reader can observe. Locate the point where a write becomes durable, a
result becomes visible, or ownership passes to another component. Follow with
the failure or concurrency case that makes the mechanism necessary.

For example, when explaining polling, establish which stream a checkpoint
identifies, what the request cursor means, and when the Colony advances it.
Then examine a storage failure, a database-session change, and a hole in the
returned sequence. Verify each answer in the relevant poller and storage code;
do not generalize one stream's behavior to all telemetry.

Treat limitations as part of the explanation. Introduce them beside the
mechanism they qualify. Keep conceptual explanations distinct from procedures
and exhaustive API reference, linking to those when needed. Support design
intent with code or RFDs and label an inferred rationale when necessary.

## Tone and pacing

Use present tense for implemented behavior and active verbs with named actors.
Give each paragraph one main idea and connect it to the next. Prefer concrete
statements about reads, writes, validation, and visibility over claims such as
"robust," "seamless," or "highly scalable."

State the model directly. Avoid promotional openings, slogans, rhetorical
questions, and announcements about what the chapter will teach. Use "can" for
a supported capability, "must" for a required condition, and "guarantees" only
for an implemented guarantee with clear scope. Distinguish measured performance
from a design goal; omit ungrounded latency, overhead, or scale claims.

Introduce a concept in plain language before its code identifier. Keep the main
explanation readable without opening every link. Place implementation anchors
near the mechanism they support or at the end of a substantial section. Explain
a constraint once and refer back to it where needed.

Use enough space to explain difficult mechanisms. Concision removes repetition
and irrelevant detail while preserving causal steps. Avoid turning chapters
into package inventories, endpoint catalogs, or lists of completed RFDs. Use
subject headings instead of repeated problem/solution/benefits labels or a
chronological account of implementation work.

## Examples and diagrams

Use a small, realistic example when identities, ordering, state transitions, or
failure behavior would otherwise stay abstract. Keep it consistent as the
explanation develops. Mark simplified examples and material omissions; do not
imply behavior the implementation does not provide.

For example, two services may both listen on port 8080 in separate network
namespaces. Use that example to distinguish a namespace-aware socket lookup
from the agent's service identity model, checking the actual keys in each.

Use diagrams to expose relationships, data flows, or transaction boundaries.
Introduce the diagram and explain the consequence it illustrates. Use tables
for genuine comparisons or ownership mappings, and lists for steps or parallel
facts. Prose should connect the chapter. Include code or SQL only when it
explains a mechanism more clearly than prose alone; link to maintained schemas.

## Final reading pass

Check whether a reader can explain the operation, its important guarantees,
and its failure behavior. Remove unsupported claims, unexplained terms,
repeated background, and details that belong in a runbook or reference. Keep
links needed to investigate the implementation and design decisions further.

## Source attribution

This guidance adapts ShowUp's architecture-docs editorial approach, informed by
these FoundationDB pages:

- [Contents](https://apple.github.io/foundationdb/contents.html): reading
  progression and separation of explanation, guidance, and reference.
- [Architecture](https://apple.github.io/foundationdb/architecture.html):
  responsibilities, transaction processing, and recovery.
- [Data Modeling](https://apple.github.io/foundationdb/data-modeling.html):
  simple models developed through representations and examples.
- [Developer Guide](https://apple.github.io/foundationdb/developer-guide.html):
  mechanisms, guarantees, and their practical consequences.

These are attribution and optional further reading, not loading instructions
or evidence about Coral. Consult them only when asked to revisit the source
material or refine its influence. Write original prose and verify Coral's
behavior in this repository.
