import json
import math
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

from app.fraud.dto import FraudAction, FraudVerdict
from app.fraud.guards import UUID_PATTERN


@dataclass(frozen=True)
class ValidationResult:
    verdict: FraudVerdict | None
    rejection_reason: str | None = None
    corrective_prompt: str = ""


def validate_verdict(
    response: str,
    *,
    score_threshold: float,
    block_threshold: float,
    max_chars: int,
    sensitive_keys: Sequence[str],
) -> ValidationResult:
    if len(response) > max_chars:
        return _rejected("response_too_large")
    try:
        parsed = json.loads(response)
    except json.JSONDecodeError:
        return _rejected("invalid_json")
    if not isinstance(parsed, dict) or not all(
        key in parsed for key in ("risk_score", "action", "reason")
    ):
        return _rejected("invalid_schema")
    action = parsed["action"]
    if action not in ("allow", "flag", "block"):
        return _rejected("invalid_action")
    score = _score(parsed["risk_score"])
    if score is None:
        return _rejected("invalid_score")
    reason = parsed["reason"]
    if not isinstance(reason, str) or not reason.strip() or len(reason) > 300:
        return _rejected("invalid_schema")
    if _contains_sensitive(parsed, sensitive_keys):
        return _rejected("sensitive_output")
    canonical: FraudAction
    if score >= block_threshold:
        canonical = "block"
    elif score >= score_threshold:
        canonical = "flag"
    else:
        canonical = "allow"
    return ValidationResult(
        verdict=FraudVerdict(
            risk_score=score,
            action=canonical,
            reason=reason.strip(),
            model_action=action,
            action_normalized=action != canonical,
        )
    )


def _score(value: Any) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    score = float(value)
    if not math.isfinite(score) or score < -0.01 or score > 1.01:
        return None
    return min(1.0, max(0.0, score))


def _contains_sensitive(value: dict[str, Any], sensitive_keys: Sequence[str]) -> bool:
    serialized = json.dumps(value, sort_keys=True)
    lowered = serialized.lower()
    return UUID_PATTERN.search(serialized) is not None or any(
        key.lower() in lowered for key in sensitive_keys
    )


def _rejected(reason: str) -> ValidationResult:
    return ValidationResult(
        verdict=None,
        rejection_reason=reason,
        corrective_prompt=(
            "Return only a JSON object with risk_score, action, and a concise reason. "
            f"Correct the previous validation failure: {reason}."
        ),
    )
