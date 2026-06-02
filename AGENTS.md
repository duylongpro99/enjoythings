# Repository Guidelines

## Project Structure & Module Organization
- **app**: FastAPI backend server, AI Agent LangGraph
- **web**: Nextjs Frontend
- **services**: Golang backend services

## Security & Configuration Tips

- **Never** commit `.env`, API keys, session databases, Chroma data, logs, or exported reports. Keep safe defaults in `.env.example` only. Do not log credentials or raw HTML; persisted state should contain extracted text and metadata only, as described in the architecture.

## Python Tooling

- Use `uv` and the project `.venv` for all Python commands, tests, dependency management, and packaging. Prefer `uv run ...`, `uv sync`, `uv add`, and `uv lock` over direct `python`, `pip`, or global environment commands.

## Core Principles

- **Simplicity First**: Make every change as simple as possible. Impact minimal code.
- **No Laziness**: Find root causes. No temporary fixes. Senior developer standards.
- **Minimat Impact**: Changes should only touch what's necessary. Avoid introducing bugs.

## Self-Improvement Loop

- After ANY correction from the user: update `docs/lessons.md` with the pattern
- Write rules for yourself that prevent the same mistake
- Ruthlessly iterate on these lessons until mistake rate drops
- Review lessons at session start for relevant project

## Demand Elegance (Balanced)

- For non-trivial changes: pause and ask "is there a more elegant way?"
- If a fix feels hacky: "Knowing everything I know now, implement the elegant solution"
- Skip this for simple, obvious fixes - don't over-engineer
- Challenge your own work before presenting it
- **Capture Lessons**: Update `docs/lessons.md` after corrections

## Engineering Principles
- Prefer designs that maximize clarity, adaptability, and change isolation while minimizing complexity and coupling.
- Preserve clear boundaries and maintain low coupling with high cohesion.
- Introduce abstractions only when they provide meaningful long-term value.
- Default using worktree when implement new feature/spec/plan of project. 
- After finish implementation, push worktree to origin and clean local worktree.
