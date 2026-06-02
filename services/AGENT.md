## Containerize

- Prioritize using Docker container to run external provider such as: Postgres, Kafka,...

## Think Before Build

- **Understand the problem**: Identify the core responsibility of this feature, its boundaries, what it owns vs. delegates, and what is likely to change vs. stay stable.
- **Reason about structure**: Determine how data, logic, and side effects should be separated. Identify what should be abstracted to vary independently, and whether creation, lifecycle, state, or event behavior needs explicit modeling. Spot where tight coupling is a risk.
- **Check your complexity**: Verify every abstraction solves a real problem in this codebase. Prefer the simpler approach unless real complexity justifies otherwise.
- **Write a design note before any code**:
    - Template:
        ```
        Problem: <structural challenge this feature presents>
        Structure: <how responsibilities are divided and why>
        Tradeoffs: <what was considered and rejected>
        ```
    - Path: docs/design-notes/[filename].md
- **The right design is the simplest one that handles real complexity without collapsing under future change**.

## Document usage
- **Document Up-to-date**: Before using any document, verify it matches the current codebase. If it references missing files, broken symbols, or outdated behavior — flag it, reconcile against live code, and update the stale sections before proceeding. Never propose a milestone based on assumptions you haven't confirmed in code.

## Engineering Principles
- Default using worktree when implement new feature/spec/plan of project. 
- After finish implementation, push worktree to origin and clean local worktree.
