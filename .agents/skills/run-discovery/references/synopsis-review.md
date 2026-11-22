# Synopsis review

Review `synopsis.md` and its rendered diagram against `discovery.md`. You wrote neither, so judge only what the pages
say. Read [synopsis.md](synopsis.md) first: its rules, and the reader it writes for, are your checklist. This file adds
the checks that only a fresh reader can make.

The synopsis allows 150 words, counting the closing link's text. Count before proposing a fix that adds words, and pay
for it elsewhere.

Report each finding with its location (a sentence, node, or edge), the problem, the evidence, and a concrete fix. Quote
the `discovery.md` line that the finding rests on, or state that the discovery has no such line. Drop any finding you
cannot locate. A clean review is a valid result; never invent a finding to fill one.

## Fidelity

- Every claim, number, and name traces to a line in `discovery.md`. A role renamed for the reader passes when it names
  the same person.
- The proposal matches **Proposed Approach**, and no rejected alternative sounds like it.
- Every point on the keep list in [synopsis.md](synopsis.md) survives.

## Spoken test

Confirm it tells what, how, and why, in three paragraphs before the closing link and in that order, each opening with a
cue a listener can hear, and that the diagram, if any, follows the how. Read it aloud as that reader. Flag every term
they would need defined ("discovery" needs none; a term from the skill itself, such as `ready`, does), every sentence a
speaker would trip over, and every pronoun or noun a listener would attach to the wrong thing, since they cannot hear a
paragraph break.

## Diagram

Judge the rendered image, and read the Mermaid source only to locate a fix.

- Read each labeled edge aloud as a sentence: source, label, target. Every sentence is true to the discovery, including
  who acts: a node that stands for a document, review, or meeting performs no action the discovery gives to a person,
  and an AI actor never reads as a person, in the diagram or the prose.
- The diagram adds no node or edge that the discovery lacks.
- Check every diagram rule in [synopsis.md](synopsis.md), then look for what a rule cannot catch: two edges that look
  alike but mean different things, a change a reader cannot spot, crossed edges, and clipped or overlapping labels.
- It earns its place by the keep test in **Decide whether to draw**; recommend removing one that fails it. When the
  synopsis has no diagram, recommend one only when the discovery qualifies under that section and a diagram would pass
  the keep test, and name the condition.

## Verdict

End with `revise` when any finding misleads the reader, states something the discovery does not, or would fail
validation. Otherwise end with `ready`. Under either verdict, list the rest, including rule slips that mislead no one,
as optional polish after the findings. A `revise` verdict orders its findings by how badly each one misleads the reader,
worst first.
