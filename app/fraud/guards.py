import re
from collections.abc import Sequence

UUID_PATTERN = re.compile(
    r"(?<![0-9a-fA-F])[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-"
    r"[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(?![0-9a-fA-F])"
)


def guard_prompt(
    prompt: str, *, max_chars: int, sensitive_keys: Sequence[str]
) -> str | None:
    if not prompt.strip():
        return "prompt_empty"
    if UUID_PATTERN.search(prompt):
        return "uuid_detected"
    lowered = prompt.lower()
    if any(key.lower() in lowered for key in sensitive_keys):
        return "sensitive_key_detected"
    if len(prompt) > max_chars:
        return "prompt_too_large"
    return None
