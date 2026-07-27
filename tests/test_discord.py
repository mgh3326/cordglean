import asyncio
import json

import httpx
import pytest

from cordglean.config import ChannelAccessPolicy, Settings
from cordglean.discord import DiscordClient


def _settings() -> Settings:
    return Settings(
        token="not-a-secret",
        api_base_url="https://discord.test/api/v10",
        channel_policy=ChannelAccessPolicy(),
    )


def test_read_channel_uses_expected_endpoint_and_query() -> None:
    async def scenario() -> None:
        async def handler(request: httpx.Request) -> httpx.Response:
            assert request.url.path == "/api/v10/channels/123/messages"
            assert dict(request.url.params) == {"limit": "1", "before": "999"}
            assert request.headers["Authorization"].startswith("Bot ")
            return httpx.Response(
                200,
                json=[
                    {
                        "id": "998",
                        "channel_id": "123",
                        "content": "",
                        "embeds": [{"description": "visible"}],
                        "attachments": [],
                    }
                ],
            )

        async with DiscordClient(
            _settings(), transport=httpx.MockTransport(handler)
        ) as client:
            result = await client.read_channel("123", limit=1, before="999")
        assert result["messages"][0]["display_text"] == "[Embed 1]\nvisible"

    asyncio.run(scenario())


def test_before_and_after_are_mutually_exclusive() -> None:
    with pytest.raises(ValueError):
        DiscordClient._message_params(10, "1", "2")


def test_api_error_does_not_include_headers() -> None:
    async def scenario() -> None:
        async def handler(_: httpx.Request) -> httpx.Response:
            return httpx.Response(
                403,
                content=json.dumps({"message": "Missing Access"}),
                headers={"content-type": "application/json"},
            )

        async with DiscordClient(
            _settings(), transport=httpx.MockTransport(handler)
        ) as client:
            with pytest.raises(RuntimeError, match="HTTP 403: Missing Access") as error:
                await client.read_channel("123")
        assert "Authorization" not in str(error.value)
        assert "not-a-secret" not in str(error.value)

    asyncio.run(scenario())


def test_rate_limit_retries_using_retry_after() -> None:
    async def scenario() -> None:
        attempts = 0
        waits: list[float] = []

        async def handler(_: httpx.Request) -> httpx.Response:
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                return httpx.Response(
                    429,
                    json={"message": "rate limited", "retry_after": 0.25},
                )
            return httpx.Response(200, json=[])

        async def fake_sleep(delay: float) -> None:
            waits.append(delay)

        async with DiscordClient(
            _settings(),
            transport=httpx.MockTransport(handler),
            sleep=fake_sleep,
        ) as client:
            result = await client.read_channel("123")
        assert result["message_count"] == 0
        assert attempts == 2
        assert waits == [0.25]

    asyncio.run(scenario())
