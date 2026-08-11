# Prose style

Governs every word a human reads: `discovery.md`, the titles, descriptions, and acceptance criteria in `work-breakdown.yaml`, and the pull request body that carries the bundle for review.

Write with vigor. Omit needless words. Be specific. A discovery is read by someone deciding whether to fund the work, so every sentence must earn its place.

## Rules

- **Use the active voice.** Not "the bundle was validated" — "validation passes".
- **Say things in positive form.** Not "this is not efficient" — "this is slow". Not "does not exist" — "is missing".
- **Omit needless words.** If five words do the job, do not spend six. Cut "in order to", "the fact that", "it should be noted that".
- **Delete qualifiers.** "Very", "rather", "quite", "fairly", "somewhat", "basically", "essentially", "actually", "simply", "just". A risk is not "pretty significant"; it is "significant", or it is quantified.
- **Keep lists parallel.** Every bullet takes the same grammatical shape. Instructions take imperative verbs: "Delete the directory", "Verify the build", "Record the decision".
- **Use the Oxford comma.**
- **Lead each section with its point.** The first sentence states the conclusion; the rest supplies the evidence. One topic per paragraph.
- **Prefer data to adjectives.** Not "the module is very large" — "the module is 4,200 lines". Not "this is risky" — name the failure and who it reaches.
- **Cite specifics.** Name the file, task, or route rather than gesturing at "the config" or "some tests". A citation a reader cannot follow is not evidence.
- **Skip the fluff.** No "I hope this helps", no "Basically", no throat-clearing preamble before the substance.

## Applied to the bundle

- **`discovery.md`** — narrative, but dense. Benefits, considerations, alternatives, assumptions, and unknowns stay as concise bullets, never paragraphs wearing bullet points.
- **`work-breakdown.yaml`** — titles are imperative and specific: "Delete the SPA fallback handler", not "SPA work". Descriptions say what changes and why. Acceptance criteria are observable and testable: state the condition a reviewer can check, not an intention. "CI passes with no Build SPA step" beats "CI should be updated".
- **Pull request body** — say what changed and why, following the repo's pull request template if one exists. Do not restate the discovery; a brief summary and link is sufficient. This is not a place to surface open questions/decisions - iron those out in the conversation.

## Revising

Editing means major surgery, not polish. Strip the draft to its skeleton and rebuild it. A good revision is shorter than the original and tells the reader more.
