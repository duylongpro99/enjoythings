from app.llm.config import ProviderConfig, ProviderRegistryConfig
from app.llm.drivers.openai_compatible import OpenAICompatibleDriver
from app.llm.errors import ProviderConfigError
from app.llm.ports import LLMPort


class DriverRegistry:
    def __init__(self, *, default_driver: LLMPort, default_provider_id: str) -> None:
        self.default_driver = default_driver
        self.default_provider_id = default_provider_id

    @classmethod
    def from_config(cls, config: ProviderRegistryConfig) -> "DriverRegistry":
        providers_by_id = {provider.id: provider for provider in config.providers}

        provider = providers_by_id.get(config.default_provider_id)
        if provider is None:
            raise ProviderConfigError(
                "LLM_DEFAULT_PROVIDER must match one configured provider id"
            )

        default_driver = _build_driver(provider)
        return cls(
            default_driver=default_driver,
            default_provider_id=config.default_provider_id,
        )


def _build_driver(provider: ProviderConfig) -> LLMPort:
    if provider.driver_type == "openai_compatible":
        return OpenAICompatibleDriver(
            base_url=provider.base_url,
            api_key=provider.api_key,
            model=provider.model,
            timeout_seconds=provider.timeout_seconds,
        )

    raise ProviderConfigError(f"unsupported driver_type: {provider.driver_type}")

