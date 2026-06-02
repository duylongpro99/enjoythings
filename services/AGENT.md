## Containerize

- Prioritize using Docker container to run external provider such as: Postgres,...

## Think Before Build

- **Understand the problem**: Identify the core responsibility of this feature, its boundaries, what it owns vs. delegates, and what is likely to change vs. stay stable.
- **Reason about structure**: Determine how data, logic, and side effects should be separated. Identify what should be abstracted to vary independently, and whether creation, lifecycle, state, or event behavior needs explicit modeling. Spot where tight coupling is a risk.
- **Check your complexity**: Verify every abstraction solves a real problem in this codebase. Prefer the simpler approach unless real complexity justifies otherwise.
- **Write a design note before any code**:
    ```
    Problem: <structural challenge this feature presents>
    Structure: <how responsibilities are divided and why>
    Tradeoffs: <what was considered and rejected>
    ```
- **The right design is the simplest one that handles real complexity without collapsing under future change**.
