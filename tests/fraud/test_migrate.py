import asyncio
from pathlib import Path

from app.fraud.repo.migrate import apply_migrations


def test_explicit_migration_command_applies_fraud_migrations_in_order(tmp_path: Path) -> None:
    (tmp_path / "000002_second.sql").write_text("SELECT 2;")
    (tmp_path / "000001_first.sql").write_text("SELECT 1;")
    connection = FakeConnection()

    asyncio.run(apply_migrations(connection, tmp_path))

    assert connection.statements == ["SELECT 1;", "SELECT 2;"]


class FakeConnection:
    def __init__(self) -> None:
        self.statements = []

    async def execute(self, statement: str) -> None:
        self.statements.append(statement)
