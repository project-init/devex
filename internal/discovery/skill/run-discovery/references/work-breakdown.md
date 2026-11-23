# Work-breakdown reference

Use `schema_version: v1` and keep provider-specific configuration outside the bundle.

```yaml
schema_version: v1

discovery:
  id: 2026-11-21-audit-logs
  title: Organization audit logs
  document: discovery.md
  synopsis: synopsis.md

items:
  - id: INIT-001
    kind: initiative
    title: Deliver organization audit logs
    description: Make security-relevant activity discoverable.
    acceptance_criteria:
      - Administrators can search supported audit events.

  - id: WI-001
    kind: research
    parent: INIT-001
    title: Validate the event schema
    description: Resolve schema and retention questions.
    depends_on: []
    labels: [security]
    estimate:
      value: 1
      unit: weeks

  - id: WI-002
    kind: task
    parent: INIT-001
    repository: acme/api
    title: Serve audit events
    description: Add the endpoint that searches audit events.
    depends_on: [WI-001]

  - id: WI-003
    kind: task
    parent: INIT-001
    repository: acme/web
    title: Show audit events to administrators
    description: Add the audit page that calls the new endpoint.
    depends_on: [WI-002]
```

## One ticket per repository

Give every `task`, `feature`, or `defect` with none of those kinds beneath it the one `repository` it changes, written
as its path on the code host, such as `acme/api`. A ticket that spans repositories cannot close with one reviewed pull
request.

When work spans repositories, split it into one item per repository under a shared parent, as WI-002 and WI-003 do. Then
chain the items with `depends_on` in the order they must land: the API that serves a field before the client that reads
it, the shared library before the services that import it. Leave items that can land in either order unchained. Jira
publishes each edge as a blocking link and GitHub lists it in the issue body, so the tracker shows which ticket unblocks
the next.

An item with a `task`, `feature`, or `defect` beneath it, such as an initiative, spans their repositories and needs
none. Name one on it only when all the work beneath it lands there; validation rejects a mismatch. A `research` item may
name one when it concerns a single repository.

## Rules

- Write `discovery.id` as lowercase letters and digits separated by single hyphens. Jira publishes it as a label, and
  labels reject whitespace.
- Name the bundle's synopsis in `discovery.synopsis`. Validation then requires the file and checks it.
- Use stable uppercase IDs containing letters, digits, hyphens, or underscores.
- Name one `repository` on every `task`, `feature`, or `defect` with none of those kinds beneath it.
- Keep labels free of whitespace. Jira rejects such labels only once it starts creating issues.
- Add the configured `agent_ready_label` only to items that meet the agent-ready conditions in the skill. Leave it off
  when in doubt.
- Use one of: `initiative`, `feature`, `task`, `defect`, or `research`.
- Give every item a nonempty title and description.
- Give initiatives no parent.
- Reference parents and dependencies by stable item ID.
- Avoid parent and dependency cycles.
- Omit `depends_on` entries naming an ancestor. Hierarchy already orders that work, and providers publish each
  dependency as a real blocking link, so an initiative would block its own child.
- Reserve `depends_on` for work that must finish first. Do not restate hierarchy, related context, or reading order.
- Express verifiable completion conditions in `acceptance_criteria`.
- Use `points`, `hours`, `days`, or `weeks` for estimates.
- Represent consequential unknowns as `research` items instead of hiding them in implementation tasks.

The CLI validates structure and graph integrity. Provider planning decides how the neutral graph maps to Jira or GitHub.
