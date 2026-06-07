import argparse
import asyncio
from pathlib import Path

MIGRATIONS = Path(__file__).with_name("migrations")


async def apply_migrations(connection, directory: Path = MIGRATIONS) -> None:
    for migration in sorted(directory.glob("*.sql")):
        await connection.execute(migration.read_text())


async def _run(database_url: str) -> None:
    import asyncpg

    connection = await asyncpg.connect(database_url)
    try:
        await apply_migrations(connection)
    finally:
        await connection.close()


def main() -> None:
    parser = argparse.ArgumentParser(description="Apply dedicated fraud audit migrations")
    parser.add_argument("--database-url", required=True)
    args = parser.parse_args()
    asyncio.run(_run(args.database_url))


if __name__ == "__main__":
    main()
