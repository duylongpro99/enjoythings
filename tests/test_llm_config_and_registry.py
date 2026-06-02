import json

import pytest

from app.llm.config import load_provider_registry_config
from app.llm.errors import ProviderConfigError
from app.llm.registry import DriverRegistry
from app.llm.drivers.openai_compatible import OpenAICompatibleDriver


def _valid_env() -> dict[str, str]:
    return {
        "LLM_PROVIDERS_JSON": json.dumps(
            {
                "providers": [
                    {
                        "id": "local",
                        "driver_type": "openai_compatible",
                        "base_url": "http://127.0.0.1:11434/v1",
                        "api_key_env": "LOCAL_LLM_API_KEY",
                        "model": "llama3.1",
                        "timeout_seconds": 30,
                    }
                ]
            }
        ),
        "LLM_DEFAULT_PROVIDER": "local",
        "LOCAL_LLM_API_KEY": "secret",
    }


def test_load_provider_registry_config_rejects_malformed_json() -> None:
    env = _valid_env()
    env["LLM_PROVIDERS_JSON"] = "{not-json"
    with pytest.raises(ProviderConfigError, match="LLM_PROVIDERS_JSON must be valid JSON"):
        load_provider_registry_config(env)


def test_load_provider_registry_config_rejects_missing_fields() -> None:
    env = _valid_env()
    env["LLM_PROVIDERS_JSON"] = json.dumps({"providers": [{"id": "local"}]})
    with pytest.raises(ProviderConfigError, match="missing required provider fields"):
        load_provider_registry_config(env)


def test_load_provider_registry_config_rejects_duplicate_provider_ids() -> None:
    env = _valid_env()
    env["LLM_PROVIDERS_JSON"] = json.dumps(
        {
            "providers": [
                {
                    "id": "local",
                    "driver_type": "openai_compatible",
                    "base_url": "http://127.0.0.1:11434/v1",
                    "api_key_env": "LOCAL_LLM_API_KEY",
                    "model": "llama3.1",
                    "timeout_seconds": 30,
                },
                {
                    "id": "local",
                    "driver_type": "openai_compatible",
                    "base_url": "https://api.openai.com/v1",
                    "api_key_env": "OPENAI_API_KEY",
                    "model": "gpt-4.1-mini",
                    "timeout_seconds": 30,
                },
            ]
        }
    )
    env["OPENAI_API_KEY"] = "secret-2"
    with pytest.raises(ProviderConfigError, match="duplicate provider id"):
        load_provider_registry_config(env)


def test_load_provider_registry_config_rejects_unsupported_driver_type() -> None:
    env = _valid_env()
    env["LLM_PROVIDERS_JSON"] = json.dumps(
        {
            "providers": [
                {
                    "id": "local",
                    "driver_type": "custom",
                    "base_url": "http://127.0.0.1:11434/v1",
                    "api_key_env": "LOCAL_LLM_API_KEY",
                    "model": "llama3.1",
                    "timeout_seconds": 30,
                }
            ]
        }
    )
    with pytest.raises(ProviderConfigError, match="unsupported driver_type"):
        load_provider_registry_config(env)


def test_load_provider_registry_config_rejects_missing_default_provider() -> None:
    env = _valid_env()
    env["LLM_DEFAULT_PROVIDER"] = "openai"
    with pytest.raises(ProviderConfigError, match="LLM_DEFAULT_PROVIDER"):
        load_provider_registry_config(env)


def test_load_provider_registry_config_rejects_missing_api_key_env_value() -> None:
    env = _valid_env()
    del env["LOCAL_LLM_API_KEY"]
    with pytest.raises(ProviderConfigError, match="missing API key env var"):
        load_provider_registry_config(env)


def test_driver_registry_resolves_default_provider_driver() -> None:
    config = load_provider_registry_config(_valid_env())
    registry = DriverRegistry.from_config(config)

    default_driver = registry.default_driver
    assert isinstance(default_driver, OpenAICompatibleDriver)
