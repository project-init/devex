# Work-breakdown reference

Use `schema_version: v1` and keep provider-specific configuration outside the bundle.

```yaml
schema_version: v1

discovery:
  id: audit-logs
  title: Organization audit logs
  document: discovery.md

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
```

## Rules

- Use stable uppercase IDs containing letters, digits, hyphens, or underscores.
- Use one of: `initiative`, `feature`, `task`, `defect`, or `research`.
- Give every item a nonempty title and description.
- Give initiatives no parent.
- Reference parents and dependencies by stable item ID.
- Avoid parent and dependency cycles.
- Omit `depends_on` entries naming an ancestor. Hierarchy already orders that work, and providers publish each dependency as a real blocking link, so an initiative would block its own child.
- Reserve `depends_on` for work that must finish first. Do not restate hierarchy, related context, or reading order.
- Express verifiable completion conditions in `acceptance_criteria`.
- Use `points`, `hours`, `days`, or `weeks` for estimates.
- Represent consequential unknowns as `research` items instead of hiding them in implementation tasks.

The CLI validates structure and graph integrity. Provider planning decides how the neutral graph maps to Jira or GitHub.
