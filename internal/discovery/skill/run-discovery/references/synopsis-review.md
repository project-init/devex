# Synopsis review

Review `synopsis.md` and its rendered diagram against `discovery.md`. You wrote neither, so judge only what the pages
say. Read [synopsis.md](synopsis.md) first: its rules, and the reader it writes for, are your checklist. This file adds
the checks that only a fresh reader can make.

Report each finding with its location (a sentence, node, or edge), the problem, the evidence, and a concrete fix. Quote
the `discovery.md` line that the finding rests on, or state that the discovery has no such line. Drop any finding you
cannot locate. A clean review is a valid result; never invent a finding to fill one.

## Fidelity

- Every claim, number, and name traces to a line in `discovery.md`. A role renamed for the reader passes when it names
  the same person.
- The proposal matches **Proposed Approach**, and no rejected alternative sounds like it.
- The load-bearing points survive: the problem, the proposal, the main reason for it, the cost or phasing, and the main
  risk.
- Every hedge survives, including each part of an assumption. A safeguard the discovery assumes runs must not read as
  certain.

## Spoken test

Read it aloud as that reader. Flag every term they would need defined ("discovery" needs none), every sentence a speaker
would trip over, and every paragraph whose opening pronoun a listener would attach to the wrong sentence.

## Diagram

Judge the rendered image, and read the Mermaid source only to locate a fix.

- Read each labeled edge aloud as a sentence: source, label, target. Every sentence is true to the discovery, including
  who acts: a node that stands for a document, review, or meeting performs no action the discovery gives to a person.
- The diagram adds no node or edge that the discovery lacks.
- Check every diagram rule in [synopsis.md](synopsis.md), then look for what a rule cannot catch: two edges that look
  alike but mean different things, a change a reader cannot spot, crossed edges, and clipped or overlapping labels.
- It earns its place. Cover the prose and read the diagram alone: recommend removing it unless it shows an order,
  branch, loop, or contrast that the prose does not. When the synopsis has no diagram but the discovery meets a
  condition in **Decide whether to draw**, recommend one and name the condition.

## Verdict

End with `revise` when any finding misleads the reader, states something the discovery does not, or would fail
validation. Otherwise end with `ready`, and list the rest, including rule slips that mislead no one, as optional polish.
A `revise` verdict orders its findings by how badly each one misleads the reader, worst first.
