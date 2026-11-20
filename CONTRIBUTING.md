[![License](https://img.shields.io/github/license/project-init/devex)](https://github.com/project-init/devex)

## How to contribute to Project Init

#### **Did you find a bug?**

- **Ensure the bug was not already reported** by searching on GitHub under
  [Issues](https://github.com/project-init/project-init/issues).

- If you're unable to find an open issue addressing the problem,
  [open a new one](https://github.com/project-init/project-init/issues/new). Be sure to include a **title and clear
  description**, as much relevant information as possible, and a **code sample** or an **executable test case**
  demonstrating the expected behavior that is not occurring.

#### **Did you write a patch that fixes a bug?**

- Open a new GitHub pull request with the patch.
- Ensure the PR description clearly describes the problem and solution. Include the relevant issue number if applicable.

#### **Did you fix whitespace, format code, or make a purely cosmetic patch?**

Changes that are cosmetic in nature and do not add anything substantial to the stability, functionality, or testability
of Project Init will generally not be accepted. This will all be handled by automated formatters and therefore any
changes to an automated formatter could be seen as substantial enough to warrant merging.

#### **Do you intend to add a new feature or change an existing one?**

- Open a Pull Request with a discovery bundle. Run `go run ./cmd/devex discovery init docs/discoveries <name>` to
  generate `discovery.md` and `work-breakdown.yaml`, then fill them in. The
  [discovery docs](cmd/devex/README.md#discovery) explain each file.
- Do not open an issue on GitHub until you have collected positive feedback about the change. GitHub issues are
  primarily intended for bug reports and fixes.

Thx! :heart: :heart: :heart:

Project Init Team
