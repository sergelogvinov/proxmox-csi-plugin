# CLAUDE.md

Instructions for Claude Code working on this project.

---

## GitHub Issues

### Creating Issues

- Use the templates in `.github/ISSUE_TEMPLATE/` when creating issues:
  - `BUG_REPORT.md` — for reporting bugs.
  - `FEATURE_REQUEST.md` — for proposing new features or enhancements.
- Every issue must have:
  - A clear, descriptive **title**.
  - A concise **description** of the problem or request.
  - **Steps to reproduce** for bugs (commands, manifests, or `kubectl` output as applicable).
  - Relevant **environment** details (plugin version, Kubernetes version, OS, CSI capacity, node info).
- Preserve the template's frontmatter (`name`, `about`, `title`) and section headings. Fill in the body, do not remove the "Community Note" section.
- Apply appropriate `labels` (e.g. `bug`, `enhancement`, `documentation`) when the issue type is clear.
- Link related issues or PRs in the description when applicable.

### Maintaining Issues

- Keep issue descriptions up to date as new information is gathered (reproduction steps, environment details, error logs).
- Add follow-up comments with new findings, test results, or workarounds instead of editing over important history.
- Close issues once resolved, and reference the PR or commit that fixed them.
- Reopen issues only if the fix is reverted or proven incomplete.

---

## Pull Requests

### Creating Pull Requests

- Use `.github/PULL_REQUEST_TEMPLATE.md` as the structure for every PR description.
- Every PR must include:
  - **What?** — a clear description of the changes made.
  - **Why?** — the reasoning or motivation, linking the related issue when applicable.
  - **Acceptance checklist** — complete the checklist items truthfully:
    - [ ] linked an issue (if applicable)
    - [ ] included tests (if applicable)
    - [ ] ran conformance (`make conformance`)
    - [ ] linted code (`make lint`)
    - [ ] ran unit tests (`make unit`)
    - [ ] disclosed AI usage honestly
- Run `make help` to discover available targets before claiming a checklist item is complete.
- Keep PRs focused: one logical change per PR. Split larger work into multiple PRs when possible.

### Maintaining Pull Requests

- Address review feedback by pushing new commits or force-pushing to the PR branch.
- Update the PR description when the scope of the change evolves so it always reflects the current diff.
- Rebase onto the target branch when requested to resolve conflicts; squash commits into a single commit.
- Ensure CI is green before requesting re-review.
