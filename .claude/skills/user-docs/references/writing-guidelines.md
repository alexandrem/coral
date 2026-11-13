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

## Tone and precision

Use present tense, active verbs, and concrete actors. Give each paragraph one
main idea. State capabilities directly, without slogans, rhetorical questions,
promotional comparisons, emoji status banners, or invented sample diagnoses.
Concision removes duplication while retaining causal steps and prerequisites.

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

Show enough output or verification commands for the user to recognize success;
do not fabricate transcripts or promise exact output that varies by deployment.
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
