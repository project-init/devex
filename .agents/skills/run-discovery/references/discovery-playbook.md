# Discovery playbook

## Idea maturity

### Rough

The user has a desired outcome or pain point but little evidence, scope, or constraint detail. Concentrate on who experiences the problem, why it matters, what success means, and which facts must be gathered.

### Partially formed

The user has a likely approach and some context. Test the approach against constraints, identify meaningful alternatives, and separate implementation detail from unresolved product or architectural decisions.

### Implementation-ready

The user has evidence, an accepted direction, and bounded scope. Confirm ownership, access, failure modes, rollout, milestones, and acceptance criteria. Avoid reopening settled questions without new evidence.

## Question selection

Ask the question whose answer most changes scope, architecture, risk, or sequencing. Ask only one question per turn. Prefer bounded choices when they are honest representations of the decision; use an open question when options would conceal important possibilities.

Questioning is a loop, not a phase. Every pass that gathers evidence can surface a new fork or kill an old premise, and each one earns another round. Reopen a settled question when new evidence undermines the basis for the original answer, and say which evidence did it.

## Readiness rubric

Proceed to final artifacts when the discovery can answer:

- What problem or opportunity is being addressed?
- Who is affected and what evidence supports the need?
- What outcome defines success?
- What approach is recommended, and why?
- What credible alternatives were considered?
- What important risks have mitigations?
- What phases and dependencies shape delivery?
- Who owns the result and who needs access?
- Which assumptions and unknowns remain?

Unknowns may remain when the user deferred them, when they are explicitly represented as research work, or when they do not jeopardize the proposed direction. An unknown the user has never seen does not qualify.

## Facilitation rules

- Treat user feedback as permission to revise earlier conclusions.
- Report a correction the moment you find one: what you got wrong, what it changes, and what you now recommend.
- Take newly discovered decisions to the user rather than filing them in the bundle or a pull request body.
- Avoid filling template sections with invented facts.
- Use concise bullets for benefits, considerations, alternatives, assumptions, and unknowns.
- Keep dates and estimates qualified when evidence is weak.
- Stop exploring when additional detail would not change the decision or work breakdown.

## Common failure

An agent asks a bounded question round, gets answers, investigates further, and discovers that a premise behind those answers was wrong. Rather than returning to the user, it silently reshapes the plan and lists the fallout as open questions in the finished document. The user then has to catch the reversal by reading carefully. Go back and re-ask instead.
