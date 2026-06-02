from contextlib import asynccontextmanager
from typing import Annotated

from fastapi import Depends, FastAPI
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, StringConstraints

from app.env import load_app_env
from app.llm.config import load_provider_registry_config
from app.llm.errors import (
    ProviderConnectionError,
    ProviderHTTPStatusError,
    ProviderMalformedStreamError,
    ProviderTimeoutError,
)
from app.llm.registry import DriverRegistry
from app.llm.service import ChatService
from app.llm.sse_presenter import stream_openai_chat_sse


class ChatRequestBody(BaseModel):
    message: Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


def _build_chat_service_from_env() -> ChatService:
    config = load_provider_registry_config()
    registry = DriverRegistry.from_config(config)
    return ChatService(registry.default_driver)


def create_app(*, chat_service: ChatService | None = None) -> FastAPI:
    load_app_env()

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        if getattr(app.state, "chat_service", None) is None:
            app.state.chat_service = _build_chat_service_from_env()
        yield

    app = FastAPI(lifespan=lifespan)

    if chat_service is not None:
        app.state.chat_service = chat_service

    def get_chat_service() -> ChatService:
        service = getattr(app.state, "chat_service", None)
        if service is None:
            raise RuntimeError("Chat service was not initialized")
        return service

    @app.post("/chat")
    async def chat(
        payload: ChatRequestBody,
        service: Annotated[ChatService, Depends(get_chat_service)],
    ):
        sse_stream = stream_openai_chat_sse(service.stream_reply(payload.message))

        try:
            first_event = await anext(sse_stream)
        except ProviderTimeoutError:
            return JSONResponse(
                status_code=504,
                content={"error": "Provider timed out after 3 attempts"},
            )
        except ProviderConnectionError:
            return JSONResponse(
                status_code=502,
                content={"error": "Provider connection failure"},
            )
        except ProviderHTTPStatusError:
            return JSONResponse(
                status_code=502,
                content={"error": "Provider returned non-success status"},
            )
        except ProviderMalformedStreamError:
            return JSONResponse(
                status_code=502,
                content={"error": "Provider returned malformed stream data"},
            )

        async def with_first_event():
            yield first_event
            async for event in sse_stream:
                yield event

        return StreamingResponse(
            with_first_event(),
            media_type="text/event-stream",
        )

    return app


app = create_app()
