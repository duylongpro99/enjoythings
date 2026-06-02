class ProviderError(Exception):
    """Base class for request-time provider failures."""


class ProviderConfigError(Exception):
    """Configuration failure while constructing provider drivers."""


class ProviderTimeoutError(ProviderError):
    """Provider request timed out."""


class ProviderConnectionError(ProviderError):
    """Provider could not be reached."""


class ProviderHTTPStatusError(ProviderError):
    """Provider responded with a non-2xx status."""


class ProviderMalformedStreamError(ProviderError):
    """Provider stream payload could not be parsed safely."""

