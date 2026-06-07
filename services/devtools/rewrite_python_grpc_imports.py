from pathlib import Path


GENERATED_ROOT = Path("app/fraud/integrations/gen")
PACKAGE_ROOT = "app.fraud.integrations.gen"


def main() -> None:
    for service in ("ledger", "verification"):
        package = GENERATED_ROOT / service / "v1"
        package.mkdir(parents=True, exist_ok=True)
        (GENERATED_ROOT / service / "__init__.py").touch()
        (package / "__init__.py").touch()

        grpc_file = package / f"{service}_pb2_grpc.py"
        source = grpc_file.read_text()
        source = source.replace(
            f"from {service}.v1 import {service}_pb2",
            f"from {PACKAGE_ROOT}.{service}.v1 import {service}_pb2",
        )
        grpc_file.write_text(source)


if __name__ == "__main__":
    main()
