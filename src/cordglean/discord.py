from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any

import httpx

from cordglean.config import Settings, validate_snowflake
from cordglean.render import render_message

MAX_RATE_LIMIT_RETRIES = 2
MAX_RATE_LIMIT_WAIT_SECONDS = 30.0


class DiscordAPIError(RuntimeError):
    """A sanitized Discord API failure suitable for an MCP tool response."""

    def __init__(
        self,
        message: str,
        *,
        status_code: int | None = None,
        retry_after: float | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.retry_after = retry_after


class DiscordClient:
    def __init__(
        self,
        settings: Settings,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
    ) -> None:
        self._settings = settings
        self._sleep = sleep
        self._request_lock = asyncio.Lock()
        self._client = httpx.AsyncClient(
            base_url=settings.api_base_url,
            headers={
                "Authorization": f"Bot {settings.token}",
                "User-Agent": "cordglean/0.1 (+https://github.com/mgh3326/cordglean)",
            },
            timeout=httpx.Timeout(20.0),
            transport=transport,
        )

    async def __aenter__(self) -> DiscordClient:
        return self

    async def __aexit__(self, *_: object) -> None:
        await self._client.aclose()

    async def _get(
        self, path: str, *, params: dict[str, str | int] | None = None
    ) -> Any:
        async with self._request_lock:
            for attempt in range(MAX_RATE_LIMIT_RETRIES + 1):
                try:
                    response = await self._client.get(path, params=params)
                except httpx.RequestError as exc:
                    raise DiscordAPIError(
                        f"Discord API request failed: {type(exc).__name__}"
                    ) from None

                if response.status_code != 429:
                    break

                retry_after = self._retry_after(response)
                if (
                    attempt == MAX_RATE_LIMIT_RETRIES
                    or retry_after > MAX_RATE_LIMIT_WAIT_SECONDS
                ):
                    raise DiscordAPIError(
                        "Discord API rate limit exceeded",
                        status_code=429,
                        retry_after=retry_after,
                    )
                await self._sleep(retry_after)

        try:
            response.raise_for_status()
        except httpx.HTTPStatusError as exc:
            message, _ = self._error_details(exc.response)
            detail = f": {message}" if message else ""
            raise DiscordAPIError(
                f"Discord API returned HTTP {exc.response.status_code}{detail}",
                status_code=exc.response.status_code,
            ) from None

        try:
            return response.json()
        except ValueError:
            raise DiscordAPIError(
                "Discord API returned invalid JSON",
                status_code=response.status_code,
            ) from None

    @staticmethod
    def _error_details(response: httpx.Response) -> tuple[str, int | None]:
        message = ""
        code = None
        try:
            payload = response.json()
            if isinstance(payload, dict):
                message = str(payload.get("message", ""))
                raw_code = payload.get("code")
                code = raw_code if isinstance(raw_code, int) else None
        except ValueError:
            pass
        return message, code

    @staticmethod
    def _retry_after(response: httpx.Response) -> float:
        raw_header = response.headers.get("retry-after")
        if raw_header is not None:
            try:
                return max(0.0, float(raw_header))
            except ValueError:
                pass
        try:
            payload = response.json()
            if isinstance(payload, dict):
                return max(0.0, float(payload.get("retry_after", 0.0)))
        except (TypeError, ValueError):
            pass
        return 1.0

    @staticmethod
    def _message_params(
        limit: int, before: str | None, after: str | None
    ) -> dict[str, str | int]:
        if not 1 <= limit <= 100:
            raise ValueError("limit must be between 1 and 100")
        if before is not None and after is not None:
            raise ValueError("before and after are mutually exclusive")
        params: dict[str, str | int] = {"limit": limit}
        if before is not None:
            params["before"] = validate_snowflake(before, "before")
        if after is not None:
            params["after"] = validate_snowflake(after, "after")
        return params

    async def read_channel(
        self,
        channel_id: str,
        *,
        limit: int = 20,
        before: str | None = None,
        after: str | None = None,
    ) -> dict[str, Any]:
        channel_id = validate_snowflake(channel_id, "channel_id")
        self._settings.channel_policy.require_channel(channel_id)
        payload = await self._get(
            f"/channels/{channel_id}/messages",
            params=self._message_params(limit, before, after),
        )
        if not isinstance(payload, list):
            raise DiscordAPIError("Discord API returned an unexpected messages payload")
        messages = [
            render_message(message)
            for message in payload
            if isinstance(message, dict)
        ]
        return {
            "channel_id": channel_id,
            "order": "newest_to_oldest",
            "message_count": len(messages),
            "page": {
                "requested_limit": limit,
                "before": before,
                "after": after,
                "newest_message_id": messages[0].get("id") if messages else None,
                "oldest_message_id": messages[-1].get("id") if messages else None,
                "possibly_more": len(messages) == limit,
            },
            "messages": messages,
        }

    async def get_message(
        self,
        channel_id: str,
        message_id: str,
        *,
        include_raw_embeds: bool = True,
    ) -> dict[str, Any]:
        channel_id = validate_snowflake(channel_id, "channel_id")
        message_id = validate_snowflake(message_id, "message_id")
        self._settings.channel_policy.require_channel(channel_id)
        payload = await self._get(f"/channels/{channel_id}/messages/{message_id}")
        if not isinstance(payload, dict):
            raise DiscordAPIError("Discord API returned an unexpected message payload")
        return render_message(payload, include_raw_embeds=include_raw_embeds)

    async def read_thread(
        self,
        thread_id: str,
        *,
        limit: int = 20,
        before: str | None = None,
        after: str | None = None,
    ) -> dict[str, Any]:
        result = await self.read_channel(
            thread_id, limit=limit, before=before, after=after
        )
        result["thread_id"] = result.pop("channel_id")
        return result

    async def list_channels(self, guild_id: str) -> dict[str, Any]:
        guild_id = validate_snowflake(guild_id, "guild_id")
        payload = await self._get(f"/guilds/{guild_id}/channels")
        if not isinstance(payload, list):
            raise DiscordAPIError("Discord API returned an unexpected channels payload")
        channels = [
            dict(channel)
            for channel in payload
            if isinstance(channel, dict)
            and isinstance(channel.get("id"), str)
            and self._settings.channel_policy.permits_channel(channel["id"])
        ]
        return {"guild_id": guild_id, "channel_count": len(channels), "channels": channels}

    async def read_user_profile(
        self, user_id: str, *, guild_id: str | None = None
    ) -> dict[str, Any]:
        user_id = validate_snowflake(user_id, "user_id")
        if guild_id is None:
            payload = await self._get(f"/users/{user_id}")
            source = "user"
        else:
            guild_id = validate_snowflake(guild_id, "guild_id")
            payload = await self._get(f"/guilds/{guild_id}/members/{user_id}")
            source = "guild_member"
        if not isinstance(payload, dict):
            raise DiscordAPIError("Discord API returned an unexpected user payload")
        return {"source": source, "profile": payload}
