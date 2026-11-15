# User Guide Writing Guidelines

Adapt the architecture-docs skill's calm, connected prose to a reader trying to
operate Coral. Assume familiarity with terminals and applications, but introduce
Coral terminology before relying on it. Write original explanations grounded in
the repository's implemented behavior.

## Develop understanding through a task

Begin with the user's goal and the model needed to make the next decision. For
example, explain that watching a service adds an explicit name and optional
health check before showing the watch command. Follow the action through its
observable result and the next useful step.

Give prerequisites before commands. Say which machine and terminal to use when
the context changes. Explain why a setting is needed and what changes when the
user chooses it. Keep conceptual context close to the procedure, linking to
architecture for mechanisms that do not help the user choose or act.

Use numbered steps for an ordered procedure and lists for parallel choices.
Use tables to compare deployment modes, credential purposes, or diagnostic
outcomes. Let prose connect the page; avoid turning it into a command catalog.
Choose headings for user goals and symptoms rather than imposing a repeated
problem/solution/benefits template.

## Shape pages and procedures

Open a page with what the reader will accomplish and, for a long page, a short
map from tasks or symptoms to sections. A first-run chapter should state what the
reader will have at the end and get to its first result by the shortest working
path; move alternatives, platform notes, and edge cases after that result.

A procedure usually runs: goal, prerequisites, steps, verification, and what to
do if it fails. Omit parts that do not apply; do not pad a short task to fit.
Every procedure ends by telling the reader what success looks like and which
command or log confirms it.

Within a section, lead with the instruction or answer. Put exceptions and
limits immediately after it, attached to the step they qualify. When a section
accumulates more than two caveats, separate them into a short list under the
instruction instead of chaining them in one paragraph, or move rare cases to
troubleshooting. Where behavior is known, state it; where the reader must
inspect something, say what to check and what result to expect.

Give each page one audience task and each paragraph one idea. Prefer a small
table over negated prose such as "X does not relocate Y"; list where each thing
does live.

## Show success

Describe what a good result contains: the fields to look at, the rows that
should appear, the log line that confirms startup. Use a short output block only
when its shape is stable, label it as illustrative, and use the shared example
names. Do not fabricate complete transcripts or promise values that vary by
deployment. When a result can be empty, say what empty means and what to check.

## Configuration, files, and examples

Help readers choose their task and file before presenting settings. When several
files serve different components, a file-purpose table with the component, the
owning account, and how to create it prevents editing the wrong one. Separate
configuration paths from persistent state, and explain which account, home
directory, and working directory determine them.

Explain precedence for the specific decision being made. State the order
explicitly, illustrate a likely conflict, say how to identify the active source,
and mention overrides that persist after switching contexts. Different decisions
(for example which Colony the CLI targets, how it connects, and how an agent
starts) can have different rules; do not merge them.

Label every YAML example with its destination file and component, including
whether a Colony file belongs to the server or a workstation. State whether an
example is a complete file or a fragment. Fragments must preserve required
existing settings and merge without duplicate keys. Keep an example focused on
one decision; put tuning in its own procedure.

Make reference tables easy to scan by component and subsystem. Explain dotted
field names as YAML nesting and identify tables using relative paths. Preserve
types, defaults, and useful qualifications when consolidating, and link to the
workflow chapter for choices and verification. Keep choices out of the reference
and field catalogs out of workflows.

For remote or multi-party setup, present trust prerequisites, alternative
methods, authentication, and verification in that order. Distinguish local names
from real identifiers, and say when an operation needs a network route beyond
the one the reader has just configured.

## Tone and precision

Use present tense, active verbs, and concrete actors. State capabilities
directly, without slogans, rhetorical questions, promotional comparisons, emoji
status banners, or invented sample diagnoses. Concision removes duplication
while retaining causal steps and prerequisites; it never removes the instruction
itself.

Use "can" for a supported capability and "must" for a required condition. Name
the scope of a guarantee. Distinguish defaults from example settings, successful
startup from available telemetry, and missing evidence from a healthy system.
Put limitations beside the action or interpretation they qualify, including
platform support, sampling, permissions, stale results, and partial failures.

Do not publish roadmap behavior as usage instructions. Explain implemented
limitations plainly and link to design history only when it helps the reader.
Avoid unmeasured overhead, latency, capacity, and automatic-diagnosis claims.

## Examples and troubleshooting

Use a small, consistent application example such as `api` and `payments`.
Commands should show a realistic successful path, with required credentials
supplied through clearly identified placeholders or earlier steps. Never include
real secrets. Explain when commands expose credentials or modify a running
system. Distinguish optional integrations from prerequisites for the core task.

Explain durations, units, target selection, and the meaning of empty results.
Keep exhaustive syntax in the single CLI reference and interface definitions in
their existing references.

Troubleshooting should move from a symptom to a discriminating check and a
specific recovery action. Check identity, configuration, connectivity, and
collection separately. Explain the effect of a reset or rotation before giving
the command; preserve data and identity when recovery does not require replacing
them. Finish a procedure with verification, not an unexplained configuration edit.

Use relative repository links without machine-specific paths or source line
numbers. Link to further detail where a user needs it. Update navigation as the
guide changes so both sequential reading and direct lookup remain useful.

## Reader pass

Before finishing, reread each changed page as a first-time user:

- The first sentence of the page and of each section states the goal or answer.
- Every command block says where it runs and is followed by what success looks like.
- Every caveat is attached to the step it affects, and no section chains more
  than two.
- No paragraph carries more than one idea, and no sentence says only that
  something is not true without saying what is.
- The page works as an entry point: prerequisites are linked, not assumed.
