import json
import os
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

from app.llm.errors import ProviderConfigError

SUPPORTED_DRIVER_TYPES = {"openai_compatible"}


@dataclass(frozen=True)
class ProviderConfig:
    id: str
    driver_type: str
    base_url: str
    api_key_env: str
    model: str
    timeout_seconds: float
    api_key: str


@dataclass(frozen=True)
class ProviderRegistryConfig:
    providers: list[ProviderConfig]
    default_provider_id: str


def load_provider_registry_config(
    environ: Mapping[str, str] | None = None,
) -> ProviderRegistryConfig:
    source = environ if environ is not None else os.environ

    raw_json = source.get("LLM_PROVIDERS_JSON")
    if not raw_json:
        raise ProviderConfigError("LLM_PROVIDERS_JSON is required")

    try:
        parsed = json.loads(raw_json)
    except json.JSONDecodeError as exc:
        raise ProviderConfigError("LLM_PROVIDERS_JSON must be valid JSON") from exc

    providers_value = parsed.get("providers") if isinstance(parsed, dict) else None
    if not isinstance(providers_value, list) or not providers_value:
        raise ProviderConfigError(
            "LLM_PROVIDERS_JSON must include a non-empty providers list"
        )

    providers: list[ProviderConfig] = []
    seen_ids: set[str] = set()

    for raw_provider in providers_value:
        provider = _parse_provider(raw_provider, source)

        if provider.id in seen_ids:
            raise ProviderConfigError(f"duplicate provider id: {provider.id}")
        seen_ids.add(provider.id)
        providers.append(provider)

    default_provider_id = source.get("LLM_DEFAULT_PROVIDER")
    if not default_provider_id:
        raise ProviderConfigError("LLM_DEFAULT_PROVIDER is required")

    if default_provider_id not in seen_ids:
        raise ProviderConfigError(
            "LLM_DEFAULT_PROVIDER must match one configured provider id"
        )

    return ProviderRegistryConfig(
        providers=providers,
        default_provider_id=default_provider_id,
    )


def _parse_provider(raw_provider: Any, environ: Mapping[str, str]) -> ProviderConfig:
    if not isinstance(raw_provider, dict):
        raise ProviderConfigError("provider entries must be JSON objects")

    required_fields = (
        "id",
        "driver_type",
        "base_url",
        "api_key_env",
        "model",
        "timeout_seconds",
    )
    missing = [field for field in required_fields if field not in raw_provider]
    if missing:
        raise ProviderConfigError(
            f"missing required provider fields: {', '.join(sorted(missing))}"
        )

    provider_id = str(raw_provider["id"]).strip()
    driver_type = str(raw_provider["driver_type"]).strip()
    base_url = str(raw_provider["base_url"]).strip().rstrip("/")
    api_key_env = str(raw_provider["api_key_env"]).strip()
    model = str(raw_provider["model"]).strip()

    try:
        timeout_seconds = float(raw_provider["timeout_seconds"])
    except (TypeError, ValueError) as exc:
        raise ProviderConfigError("timeout_seconds must be a positive number") from exc

    if timeout_seconds <= 0:
        raise ProviderConfigError("timeout_seconds must be a positive number")

    if not provider_id or not base_url or not api_key_env or not model:
        raise ProviderConfigError("provider string fields must be non-empty")

    if driver_type not in SUPPORTED_DRIVER_TYPES:
        raise ProviderConfigError(f"unsupported driver_type: {driver_type}")

    api_key = environ.get(api_key_env)
    if not api_key:
        raise ProviderConfigError(f"missing API key env var: {api_key_env}")

    return ProviderConfig(
        id=provider_id,
        driver_type=driver_type,
        base_url=base_url,
        api_key_env=api_key_env,
        model=model,
        timeout_seconds=timeout_seconds,
        api_key=api_key,
    )
