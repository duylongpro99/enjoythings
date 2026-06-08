FROM ghcr.io/astral-sh/uv:0.7.13-python3.13-bookworm-slim

WORKDIR /workspace
COPY pyproject.toml uv.lock README.md ./
COPY app ./app
RUN uv sync --frozen --no-dev

CMD ["sh", "-ec", "uv run fraud-migrate --database-url \"$FRAUD_DATABASE_URL\" && exec uv run fraud-worker"]
