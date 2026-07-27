#!/usr/bin/env python3
"""Call read_channel exactly once through cordglean's real stdio MCP transport."""

from __future__ import annotations

import argparse
import asyncio
import json
import os
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.types import TextContent


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("channel_id")
    parser.add_argument("--limit", type=int, default=5)
    parser.add_argument("--before")
    parser.add_argument("--after")
    return parser.parse_args()


async def run(args: argparse.Namespace) -> None:
    project_root = Path(__file__).resolve().parents[1]
    server = StdioServerParameters(
        command="uv",
        args=["--directory", str(project_root), "run", "cordglean"],
        env=dict(os.environ),
    )
    arguments: dict[str, object] = {
        "channel_id": args.channel_id,
        "limit": args.limit,
    }
    if args.before:
        arguments["before"] = args.before
    if args.after:
        arguments["after"] = args.after

    async with (
        stdio_client(server) as (read_stream, write_stream),
        ClientSession(read_stream, write_stream) as session,
    ):
        await session.initialize()
        tools = await session.list_tools()
        actual_tools = {tool.name for tool in tools.tools}
        expected_tools = {
            "get_message",
            "list_channels",
            "read_channel",
            "read_thread",
            "read_user_profile",
        }
        if actual_tools != expected_tools:
            raise RuntimeError(f"unexpected MCP tool surface: {sorted(actual_tools)}")
        result = await session.call_tool("read_channel", arguments=arguments)

    if result.isError:
        text = "\n".join(
            block.text for block in result.content if isinstance(block, TextContent)
        )
        raise RuntimeError(text or "read_channel failed")
    if result.structuredContent is not None:
        print(json.dumps(result.structuredContent, ensure_ascii=False, indent=2))
        return
    for block in result.content:
        if isinstance(block, TextContent):
            print(block.text)


if __name__ == "__main__":
    asyncio.run(run(parse_args()))
