# Lessons

- When asked to push after completing feature work, verify the intended branch before pushing. If the feature was implemented on a separate branch, treat "push to origin" as likely referring to that feature branch unless the user explicitly names `master` or the current branch.
- Before proposing a new agent framework or service, inspect and reuse the repository's existing agent runtime and LLM adapter boundary. Keep model-provider flexibility behind the established adapter contract instead of duplicating provider selection in another language or service.
- For polyglot service integration, isolate language- and protocol-specific details behind a thin client layer. Agent workflows should call domain service methods, not inline gRPC/protobuf operations, so the migration and test surface stays small.
- When the user asks to enhance specs, keep the work at the specification level. Do not transition into implementation-plan creation unless the user explicitly requests a plan.
