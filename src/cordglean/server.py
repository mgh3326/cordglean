from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any

from mcp.server.fastmcp import Context, FastMCP
from mcp.server.session import ServerSession

from cordglean.config import Settings
from cordglean.discord import DiscordClient


@dataclass
class AppContext:
    discord: DiscordClient


@asynccontextmanager
async def app_lifespan(_: FastMCP) -> AsyncIterator[AppContext]:
    settings = Settings.from_environment()
    async with DiscordClient(settings) as client:
        yield AppContext(discord=client)

mcp = FastMCP(
    "cordglean",
    instructions=(
        "Read-only Discord access. Embed-only messages are exposed through "
        "display_text, rendered_embeds, and raw embeds. No mutation tools exist."
    ),
    lifespan=app_lifespan,
)


async def _with_client(
    ctx: Context[ServerSession, AppContext],
    method: str,
    **kwargs: Any,
) -> dict[str, Any]:
    operation = getattr(ctx.request_context.lifespan_context.discord, method)
    return await operation(**kwargs)


@mcp.tool()
async def read_channel(
    channel_id: str,
    ctx: Context[ServerSession, AppContext],
    limit: int = 20,
    before: str | None = None,
    after: str | None = None,
) -> dict[str, Any]:
    """Read messages, complete embeds, and attachment metadata from a channel."""
    return await _with_client(
        ctx,
        "read_channel",
        channel_id=channel_id,
        limit=limit,
        before=before,
        after=after,
    )


@mcp.tool()
async def get_message(
    channel_id: str,
    message_id: str,
    ctx: Context[ServerSession, AppContext],
    include_raw_embeds: bool = True,
) -> dict[str, Any]:
    """Read one message with complete embed rendering and optional raw embeds."""
    return await _with_client(
        ctx,
        "get_message",
        channel_id=channel_id,
        message_id=message_id,
        include_raw_embeds=include_raw_embeds,
    )


@mcp.tool()
async def read_thread(
    thread_id: str,
    ctx: Context[ServerSession, AppContext],
    limit: int = 20,
    before: str | None = None,
    after: str | None = None,
) -> dict[str, Any]:
    """Read messages and complete embeds from a Discord thread."""
    return await _with_client(
        ctx,
        "read_thread",
        thread_id=thread_id,
        limit=limit,
        before=before,
        after=after,
    )


@mcp.tool()
async def list_channels(
    guild_id: str,
    ctx: Context[ServerSession, AppContext],
) -> dict[str, Any]:
    """List channels visible to the bot in a guild."""
    return await _with_client(ctx, "list_channels", guild_id=guild_id)


@mcp.tool()
async def read_user_profile(
    user_id: str,
    ctx: Context[ServerSession, AppContext],
    guild_id: str | None = None,
) -> dict[str, Any]:
    """Read a Discord user, or their richer guild member profile when given a guild."""
    return await _with_client(
        ctx,
        "read_user_profile",
        user_id=user_id,
        guild_id=guild_id,
    )


def main() -> None:
    """Run cordglean over stdio."""
    mcp.run(transport="stdio")
