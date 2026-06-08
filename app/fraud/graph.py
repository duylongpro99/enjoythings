import asyncio
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from time import monotonic
from typing import Protocol
from uuid import uuid4

from langgraph.graph import END, StateGraph

from app.fraud.config import FraudConfig
from app.fraud.dto import (
    FraudOutcome,
    FraudScoreRequest,
    FraudSession,
    SanitizedTransactionFacts,
    TransactionHistoryEntry,
)
from app.fraud.guards import guard_prompt
from app.fraud.instruction import SYSTEM_INSTRUCTION
from app.fraud.metrics import DEFAULT_METRICS, FraudMetrics
from app.fraud.ports import CompletionPort, FraudDataPort, FraudSessionStore
from app.fraud.tracing import start_span
from app.fraud.validator import ValidationResult, validate_verdict
from app.llm.types import ChatMessage


class Clock(Protocol):
    def datetime(self) -> datetime: ...

    def monotonic(self) -> float: ...


class SystemClock:
    def datetime(self) -> datetime:
        return datetime.now(UTC)

    def monotonic(self) -> float:
        return monotonic()


@dataclass
class GraphState:
    request: FraudScoreRequest
    session: FraudSession | None = None
    facts: SanitizedTransactionFacts | None = None
    prompt: str = ""
    messages: list[ChatMessage] | None = None
    raw_response: str = ""
    validation: ValidationResult | None = None
    outcome: FraudOutcome | None = None
    attempt: int = 1
    next_step: str = ""


class FraudScoringGraph:
    def __init__(
        self,
        *,
        data: FraudDataPort,
        completion: CompletionPort,
        store: FraudSessionStore,
        config: FraudConfig,
        clock: Clock | None = None,
        system_instruction: str = SYSTEM_INSTRUCTION,
        metrics: FraudMetrics = DEFAULT_METRICS,
    ) -> None:
        self._data = data
        self._completion = completion
        self._store = store
        self._config = config
        self._clock = clock or SystemClock()
        self._system_instruction = system_instruction
        self._metrics = metrics
        self._graph = self._build_graph()

    async def score(self, request: FraudScoreRequest) -> FraudOutcome:
        state = GraphState(request=request)
        try:
            final_state = await self._graph.ainvoke(state)
            outcome = final_state.get("outcome")
            if isinstance(outcome, FraudOutcome):
                return outcome
            return FraudOutcome(action=None, reason_code="audit_failed")
        except Exception:
            return FraudOutcome(action=None, reason_code="audit_failed")

    def _build_graph(self):
        graph = StateGraph(GraphState)
        graph.add_node("create_session", self._node_create_session)
        graph.add_node("build_sanitized_context", self._node_build_sanitized_context)
        graph.add_node("enrich_transaction", self._node_enrich_transaction)
        graph.add_node("build_prompt", self._node_build_prompt)
        graph.add_node("input_guard", self._node_input_guard)
        graph.add_node("call_llm", self._node_call_llm)
        graph.add_node("validate_verdict", self._node_validate_verdict)
        graph.add_node("retry_prompt", self._node_retry_prompt)
        graph.add_node("complete_session", self._node_complete_session)
        graph.set_entry_point("create_session")
        route_map = {
            "build_sanitized_context": "build_sanitized_context",
            "enrich_transaction": "enrich_transaction",
            "build_prompt": "build_prompt",
            "input_guard": "input_guard",
            "call_llm": "call_llm",
            "validate_verdict": "validate_verdict",
            "retry_prompt": "retry_prompt",
            "complete_session": "complete_session",
            "done": END,
        }
        for node in (
            "create_session",
            "build_sanitized_context",
            "enrich_transaction",
            "build_prompt",
            "input_guard",
            "call_llm",
            "validate_verdict",
            "retry_prompt",
            "complete_session",
        ):
            graph.add_conditional_edges(node, _route, route_map)
        return graph.compile()

    async def _node_create_session(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.create_session", payment_id=state.request.payment_id):
            await self._create_session(state)
            if state.session and state.session.completed and state.session.outcome:
                state.outcome = state.session.outcome
                state.next_step = "done"
            else:
                state.next_step = "build_sanitized_context"
        return state

    async def _node_build_sanitized_context(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.build_sanitized_context", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            await self._build_sanitized_context(state)
            state.next_step = "enrich_transaction"
        return state

    async def _node_enrich_transaction(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.enrich_transaction", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            try:
                await self._enrich_transaction(state)
                for method in ("history", "velocity", "kyc"):
                    self._metrics.enrichment_call(method, "success")
                state.next_step = "build_prompt"
            except Exception:
                for method in ("history", "velocity", "kyc"):
                    self._metrics.enrichment_call(method, "failure")
                state.outcome = FraudOutcome(action=None, reason_code="enrichment_failed")
                state.next_step = "complete_session"
        return state

    async def _node_build_prompt(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.build_prompt", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            await self._build_prompt(state)
            state.next_step = "input_guard"
        return state

    async def _node_input_guard(self, state: GraphState) -> GraphState:
        with start_span("fraud.input_guard", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            rejection = await self._input_guard(state, attempt=state.attempt)
            if rejection is None:
                state.next_step = "call_llm"
            else:
                self._metrics.callback_rejection("input_guard", rejection)
                state.outcome = FraudOutcome(action=None, reason_code="prompt_rejected")
                state.next_step = "complete_session"
        return state

    async def _node_call_llm(self, state: GraphState) -> GraphState:
        with start_span("fraud.llm.complete", payment_id=state.request.payment_id, fraud_session_id=_session_id(state), provider_id=_metadata(self._completion, "provider_id"), model_id=_metadata(self._completion, "model_id")):
            if await self._call_llm(state, attempt=state.attempt):
                state.next_step = "validate_verdict"
            else:
                state.outcome = FraudOutcome(action=None, reason_code="model_failed")
                state.next_step = "complete_session"
        return state

    async def _node_validate_verdict(self, state: GraphState) -> GraphState:
        with start_span("fraud.output_validate", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            if await self._validate_verdict(state, attempt=state.attempt):
                state.next_step = "complete_session"
            elif state.attempt == 1:
                state.next_step = "retry_prompt"
            else:
                state.outcome = FraudOutcome(action=None, reason_code="validation_failed")
                state.next_step = "complete_session"
        return state

    async def _node_retry_prompt(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.retry_prompt", payment_id=state.request.payment_id, fraud_session_id=_session_id(state)):
            await self._retry_prompt(state)
            state.attempt = 2
            state.next_step = "input_guard"
        return state

    async def _node_complete_session(self, state: GraphState) -> GraphState:
        with start_span("fraud.graph.complete_session", payment_id=state.request.payment_id, fraud_session_id=_session_id(state), verdict_action=(state.outcome.action if state.outcome else "")):
            state.outcome = await self._complete_session(state, state.outcome)
            state.next_step = "done"
        return state

    async def _create_session(self, state: GraphState) -> None:
        state.session = await self._store.claim_session(state.request)
        await self._audit(
            state,
            node="create_session",
            outcome="completed",
            source_event_id=state.request.event_id,
        )

    async def _build_sanitized_context(self, state: GraphState) -> None:
        await self._audit(
            state,
            node="build_sanitized_context",
            outcome="completed",
            labels=("current_payment", "sender", "recipient"),
        )

    async def _enrich_transaction(self, state: GraphState) -> None:
        request = state.request
        history, velocity, kyc = await asyncio.gather(
            self._data.get_transaction_history(
                request.from_wallet_id, self._config.history_limit, request.trace_id
            ),
            self._data.get_velocity_metrics(request.from_wallet_id, request.trace_id),
            self._data.get_kyc_status(request.user_id, request.trace_id),
        )
        ordered_history = tuple(
            sorted(history, key=lambda entry: entry.occurred_at, reverse=True)
        )
        state.facts = SanitizedTransactionFacts(
            amount_cents=request.amount_cents,
            currency=request.currency,
            sender_kyc_status=kyc.status,
            history=ordered_history,
            velocity=velocity,
        )
        await self._audit(
            state,
            node="enrich_transaction",
            outcome="completed",
            history_count=len(ordered_history),
            sanitized_facts=_json_safe(asdict(state.facts)),
            enrichment={
                "history_count": len(ordered_history),
                "kyc_available": bool(kyc.status),
                "velocity_available": True,
            },
        )

    async def _build_prompt(self, state: GraphState) -> None:
        if state.facts is None:
            raise RuntimeError("sanitized facts are required before prompt construction")
        state.prompt = build_prompt(state.facts)
        state.messages = messages(state.prompt, system_instruction=self._system_instruction)
        await self._audit(state, node="build_prompt", outcome="completed")

    async def _input_guard(self, state: GraphState, *, attempt: int) -> str | None:
        if state.messages is None:
            raise RuntimeError("messages are required before input guard")
        rejection = guard_prompt(
            "\n".join(message.content for message in state.messages),
            max_chars=self._config.prompt_max_chars,
            sensitive_keys=self._config.sensitive_keys,
        )
        event: dict[str, object] = {"attempt": attempt}
        if rejection is None:
            event["outcome"] = "accepted"
        else:
            event.update(
                {
                    "outcome": "rejected",
                    "rejection_phase": "before",
                    "rejection_code": rejection,
                }
            )
        await self._audit(state, node="input_guard", **event)
        return rejection

    async def _call_llm(self, state: GraphState, *, attempt: int) -> bool:
        if state.messages is None:
            raise RuntimeError("messages are required before model call")
        started = self._clock.monotonic()
        try:
            state.raw_response = await self._completion.complete(state.messages)
        except Exception:
            elapsed = self._clock.monotonic() - started
            self._metrics.model_latency(
                _metadata(self._completion, "provider_id") or "unknown",
                _metadata(self._completion, "model_id") or "unknown",
                elapsed,
            )
            await self._audit(
                state,
                node="call_llm",
                outcome="failed",
                attempt=attempt,
                provider_id=_metadata(self._completion, "provider_id"),
                model_id=_metadata(self._completion, "model_id"),
                latency_ms=round(elapsed * 1000),
            )
            return False
        elapsed = self._clock.monotonic() - started
        self._metrics.model_latency(
            _metadata(self._completion, "provider_id") or "unknown",
            _metadata(self._completion, "model_id") or "unknown",
            elapsed,
        )
        await self._audit(
            state,
            node="call_llm",
            outcome="completed",
            attempt=attempt,
            provider_id=_metadata(self._completion, "provider_id"),
            model_id=_metadata(self._completion, "model_id"),
            latency_ms=round(elapsed * 1000),
            raw_response=state.raw_response,
        )
        return True

    async def _validate_verdict(self, state: GraphState, *, attempt: int) -> bool:
        state.validation = validate_verdict(
            state.raw_response,
            score_threshold=self._config.score_threshold,
            block_threshold=self._config.block_threshold,
            max_chars=self._config.response_max_chars,
            sensitive_keys=self._config.sensitive_keys,
        )
        if state.validation.verdict is None:
            self._metrics.callback_rejection(
                "output_validator",
                state.validation.rejection_reason or "invalid_schema",
            )
            await self._audit(
                state,
                node="validate_verdict",
                outcome="rejected",
                attempt=attempt,
                rejection_phase="after",
                rejection_code=state.validation.rejection_reason or "invalid_schema",
            )
            return False
        verdict = state.validation.verdict
        self._metrics.risk_score(verdict.risk_score)
        state.outcome = FraudOutcome(action=verdict.action, verdict=verdict)
        await self._audit(
            state,
            node="validate_verdict",
            outcome="accepted",
            attempt=attempt,
            action=verdict.action,
            model_action=verdict.model_action,
            action_normalized=verdict.action_normalized,
        )
        return True

    async def _retry_prompt(self, state: GraphState) -> None:
        if state.facts is None or state.validation is None:
            raise RuntimeError("facts and validation are required before retry prompt")
        state.prompt = (
            f"{state.validation.corrective_prompt}\n"
            f"Sanitized facts: {facts_sentence(state.facts)}"
        )
        state.messages = messages(state.prompt, system_instruction=self._system_instruction)
        state.raw_response = ""
        await self._audit(
            state,
            node="retry_prompt",
            outcome="completed",
            rejection_code=state.validation.rejection_reason,
        )

    async def _complete_session(
        self, state: GraphState, outcome: FraudOutcome | None
    ) -> FraudOutcome:
        if state.session is None:
            return FraudOutcome(action=None, reason_code="audit_failed")
        if outcome is None:
            outcome = FraudOutcome(action=None, reason_code="audit_failed")
        await self._audit(
            state,
            node="complete_session",
            outcome="completed",
            action=outcome.action or "fail_open",
            reason_code=outcome.reason_code or "",
        )
        completed = await self._store.complete_session(state.session, outcome)
        state.session = completed
        state.outcome = outcome
        return outcome

    async def _audit(self, state: GraphState, *, node: str, **event: object) -> None:
        if state.session is None:
            return
        with start_span("fraud.audit.write", payment_id=state.request.payment_id, fraud_session_id=state.session.session_id, operation=node, outcome=str(event.get("outcome", ""))):
            await self._store.append_event(
                state.session.session_id,
                {
                    "node": node,
                    "occurred_at": self._clock.datetime().isoformat(),
                    **event,
                },
            )


class InMemoryFraudGraphSessionStore:
    def __init__(self) -> None:
        self._sessions: dict[str, FraudSession] = {}
        self.events: dict[str, list[dict[str, object]]] = {}

    async def claim_session(self, request: FraudScoreRequest) -> FraudSession:
        session = self._sessions.get(request.event_id)
        if session is not None:
            return session
        session = FraudSession(
            session_id=str(uuid4()),
            source_event_id=request.event_id,
            payment_id=request.payment_id,
        )
        self._sessions[request.event_id] = session
        return session

    async def append_event(self, session_id: str, event: dict[str, object]) -> None:
        self.events.setdefault(session_id, []).append(dict(event))

    async def complete_session(
        self, session: FraudSession, outcome: FraudOutcome
    ) -> FraudSession:
        completed = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=True,
            outcome=outcome,
            output_event_type=_output_event_type(outcome),
            output_published=session.output_published,
        )
        self._sessions[session.source_event_id] = completed
        return completed

    async def mark_published(self, session: FraudSession) -> FraudSession:
        published = FraudSession(
            session_id=session.session_id,
            source_event_id=session.source_event_id,
            payment_id=session.payment_id,
            completed=session.completed,
            outcome=session.outcome,
            output_event_type=session.output_event_type,
            output_published=True,
        )
        self._sessions[session.source_event_id] = published
        return published

    async def renew_lease(self, session: FraudSession) -> None:
        return None

    async def release_lease(self, session: FraudSession) -> None:
        return None


def messages(
    prompt: str, *, system_instruction: str = SYSTEM_INSTRUCTION
) -> list[ChatMessage]:
    return [
        ChatMessage(role="system", content=system_instruction),
        ChatMessage(role="user", content=prompt),
    ]


def build_prompt(facts: SanitizedTransactionFacts) -> str:
    return (
        "current_payment amount "
        f"{facts.amount_cents} {facts.currency}; sender kyc {facts.sender_kyc_status}; "
        "recipient semantic label recipient; "
        f"{facts_sentence(facts)}"
    )


def facts_sentence(facts: SanitizedTransactionFacts) -> str:
    history = ", ".join(_history_sentence(entry) for entry in facts.history) or "none"
    return (
        f"velocity last hour count {facts.velocity.transactions_last_hour}, "
        f"velocity last hour cents {facts.velocity.amount_last_hour_cents}, "
        f"average 30d cents {facts.velocity.average_amount_30d_cents}, "
        f"distinct recipients 30d {facts.velocity.distinct_recipients_30d}, "
        f"recent history {history}"
    )


def _output_event_type(outcome: FraudOutcome) -> str:
    if outcome.action in ("flag", "block"):
        return "fraud.flagged"
    if outcome.reason_code:
        return "fraud.error"
    return ""


def _session_id(state: GraphState) -> str:
    return state.session.session_id if state.session is not None else ""


def _json_safe(value):
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, dict):
        return {key: _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(item) for item in value]
    return value


def _history_sentence(entry: TransactionHistoryEntry) -> str:
    return (
        f"{entry.direction} {entry.amount_cents} {entry.currency} "
        f"at {entry.occurred_at.isoformat()}"
    )


def _metadata(completion: CompletionPort, name: str) -> str:
    value = getattr(completion, name, "")
    return value if isinstance(value, str) else ""


def _latency_ms(started: float, finished: float) -> int:
    return max(0, round((finished - started) * 1000))


def _route(state: GraphState) -> str:
    return state.next_step
