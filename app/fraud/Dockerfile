FROM ghcr.io/astral-sh/uv:0.7.13-python3.13-bookworm-slim

WORKDIR /workspace
COPY pyproject.toml uv.lock README.md ./
COPY app ./app
RUN uv sync --frozen --no-dev

# The environment is complete at build time. Without this, every `uv run` (the
# entrypoint and each healthcheck) re-syncs against the lockfile, pulls the dev
# group from the network, and concurrent runs block one another on the lock.
ENV UV_NO_SYNC=1

CMD ["sh", "-ec", "uv run fraud-migrate --database-url \"$FRAUD_DATABASE_URL\" && exec uv run fraud-worker"]
