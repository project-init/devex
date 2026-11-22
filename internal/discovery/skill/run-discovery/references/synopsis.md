# Synopsis

`synopsis.md` is the discovery told aloud: what you would say to a colleague who gives you one minute. A reader who
stops there must still know what we propose, how it works, and why it is the right call. Write it last, after
`discovery.md` is accepted, because a synopsis summarizes decisions; it never makes them.

## Tell it as what, how, and why

Write for the person who decides whether to fund the work. They know the business and everyday tools such as pull
requests and issue trackers, but not this codebase or its vocabulary.

Tell the story in three paragraphs before the closing link, in this order. Open each with a spoken cue that names its
beat, so a listener follows the structure without a heading:

1. **What**: the change we propose and the problem it answers. Name who hurts and what it costs them, with the
   discovery's number for that cost or its symptom. When the discovery states no cost, state the symptom; never infer
   one. Open with a cue such as "We propose…".
2. **How**: how the change works and what it takes. Give only as much mechanism as the reader needs, then the phases or
   effort from **Phasing**. Open with a cue that names the change, such as "Here's how the new check works:". The
   diagram, when you draw one, follows this paragraph.
3. **Why**: why this way wins. Take the reason from **Proposed Approach** or **Benefits**, and when you name an
   alternative, pick one the discovery says lost and whose flaw answers that reason. Name it fully, as in "rewriting the
   billing service in place", so a listener cannot mistake it for the proposal. Close on the one risk worth watching
   and, when the discovery says, how it is handled. Open with a cue that names the change, such as "We chose the new
   check because…".

A recommendation to do nothing fills the same beats: what we recommend, how things run if we keep them and what that
costs, and why it wins, closing on what would change the answer.

Vary the cues to fit the story, but open each beat with one.

The **keep list** names what the prose must state, since a listener cannot hear a diagram, and what survives every cut:

- what we propose, who it hurts today, and what it costs them or the symptom, or, for a recommendation to do nothing,
  what we recommend;
- how it works and what it takes, or, for a recommendation to do nothing, how things run today and what that costs;
- the reason it wins;
- the main risk, with its handling when the discovery gives one, or, for a recommendation to do nothing, what would
  change the answer;
- every hedge, including each part of an assumption, so a safeguard the discovery assumes runs never reads as certain.

The diagram may add the shape of the how: an order, a branch, a loop, a contrast, or where parts connect.

- Aim for 30 to 60 seconds of speech: 75 to 150 words. `devex discovery validate` counts the prose and the closing link,
  skips the title and diagram, and rejects more than 150 words.
- Write sentences you would say out loud. Use "we" and plain verbs. Skip headings, bullets, tables, and bold labels; a
  speaker never announces "What:".
- When the word limit forces a cut, cut alternatives first, then trim mechanism, but never below a sentence that says
  how it works. Keep every point on the keep list.
- Use the reader's words. Say "discovery", not "bundle"; name things, never files. When a term needs a definition,
  replace the term. Rename a role the same way when the discovery's word would mislead, as "author" for the person
  directing the agent, provided it names the same person. Say what kind of actor does a step when a reader would guess
  wrong: "an AI reviewer", not "a reviewer".
- Open each beat with its cue, never "It" or "This". A listener cannot hear the paragraph break, so a pronoun attaches
  to the wrong sentence.
- End with `[Read the full discovery](discovery.md)`.

Trace every claim to `discovery.md`. The synopsis compresses; it never adds a fact, number, decision, or option the
discovery lacks. Carry every hedge with its claim, wherever the discovery puts it: "typically" stays, and when the
discovery assumes a safeguard runs, say "when", not "always". Place a hedge mid-sentence, and end the sentence on what
the reader should remember. When the summary needs something the discovery lacks, fix the discovery and take the change
to the user.

## Decide whether to draw

Draw a diagram when a picture shows the change faster than prose can. A change confined to one component, a policy or
process decision, and a recommendation to do nothing never qualify. These changes usually do:

- The proposal adds, removes, or reroutes connections between existing parts, and the reader needs to see where.
- One structure replaces another, and the contrast is the point.
- Work passes through four or more steps or actors, with an order, branch, or loop that matters.
- An entity's lifecycle gains, loses, or reorders states.

Then apply the keep test: keep the diagram when it gives a reader, at a glance, a shape the prose must spell out piece
by piece, whether an order, a branch, a loop, a contrast, or where parts connect. Delete it otherwise. Replace the
template's placeholder comment with the diagram, or delete the comment when you skip it.

## Draw a snapshot

Write Mermaid in a fenced `mermaid` block between the how and why paragraphs, where it illustrates the how; GitHub
renders it in the pull request and in issues. Pick the type by what changes:

| What changes                          | Diagram                                                   |
| ------------------------------------- | --------------------------------------------------------- |
| How components connect and work flows | `flowchart LR`; `flowchart TB` past four nodes in a chain |
| One structure replaces another        | `flowchart LR` with two subgraphs, `Today` and `Proposed` |
| Messages between actors over time     | `sequenceDiagram`                                         |
| An entity's lifecycle                 | `stateDiagram-v2`                                         |

`devex discovery validate` accepts one diagram of these types, or their `graph` and `stateDiagram` aliases, and rejects
every other type.

- Show one idea: the change. Draw the parts the change touches and their nearest neighbors, never the whole system. Drop
  a step the change leaves untouched unless the reader needs it to find their place.
- Keep to 10 nodes and 12 edges. A diagram that needs more is drawing the system; cut it back to the change.
- Name nodes in words the reader uses: the service, team, document, or person, never a file, class, or table.
- Keep every node and edge label to four words, not counting a change marker. Label an edge with a verb phrase, as in
  `-->|publishes|`, a condition, or what returns. Leave a plain next-step edge unlabeled rather than filling it with
  "leads to".
- Point each edge the way the work flows, and read each labeled edge aloud as a sentence: source, label, target. Every
  sentence must be true to the discovery. When a person performs a step, name the person in the node, as in
  `M["Maintainer publishes"]`, so no edge credits a document or a meeting with an action.
- Draw the step every branch shares before the split, and branch only on what differs.
- When the discovery names no actor for a step, name the step itself, as in `Publish`, rather than inventing one.
- Draw a review, check, or approval as a step on the path, as in `Draft --> Review --> Published`. A side arrow into a
  node from off the path reads as another input. A loop back to the step under review is fine; label it with what
  returns, as in `-->|returns findings|`.
- Label each branch of an either-or split with its condition, as in `-->|on GitHub|` and `-->|on Jira|`. Identical
  labels on a fan-out read as "both".
- Use `flowchart TB` for a chain longer than four nodes or one with a loop. A wide left-to-right strip shrinks to
  unreadable text in a pull request, and back edges clip their labels.
- Mark what changes in the label: `"(new)"` for a part that did not exist, including one inside a changed part,
  `"(changed)"` for an existing part that behaves differently, such as an API that now returns a cursor, or
  `"(removed)"` with a dashed stroke from `classDef removed stroke-dasharray: 5 5`. A label needs no legend and reads in
  light and dark themes. Mark nothing when everything shown is new, and let the subgraphs carry the contrast in a
  `Today` and `Proposed` diagram. Keep parentheses for these markers, so no other aside reads as one.
- Avoid fill colors and custom stroke widths. GitHub renders light and dark themes, and a fixed style breaks one.
- Give a thing the same name wherever the prose and the diagram both mention it.
- Quote labels that contain punctuation, as in `A["Ingest (v2)"]`.
- Leave out titles, legends, and notes.

## Review before presenting

Most readers read only the synopsis, and a reader who skims remembers the diagram. Review both before the user sees
them.

1. Run `devex discovery validate <bundle-directory>` and fix every error.
2. Render the diagram outside the bundle, so no image is committed:

   ```bash
   npx -y @mermaid-js/mermaid-cli@12.2.0 -i <bundle-directory>/synopsis.md -o <scratch-directory>/synopsis.md -e png -s 2
   ```

   Render into an empty scratch directory; mermaid-cli writes `synopsis-1.png` beside the output file. A failed render
   is a syntax error; fix it. When Node is missing, tell the user the diagram is unverified and ask them to check the
   pull request's rendered preview.

3. Look at the rendered image yourself. Fix crossed edges, clipped or overlapping labels, arrows that point the wrong
   way, a check drawn as a side input, and changes a reader cannot spot, before asking anyone else to look.
4. Get an independent review. Give a fresh reviewer, a subagent when the harness supports one, only `discovery.md`,
   `synopsis.md`, the rendered image, this file, and [synopsis-review.md](synopsis-review.md). Withhold your drafts and
   reasoning: a reviewer who knows what you meant reads that meaning into the page.
5. Verify each finding against `discovery.md` before acting on it, and drop any you cannot tie to a sentence, node, or
   edge. Fix the rest, render again, and send the changed text and image back for review.
6. Stop when a round returns `ready`, or after three rounds. Take anything still open to the user rather than deciding
   it alone.
7. Present the synopsis and its rendered diagram to the user for approval, as you present other design sections.
