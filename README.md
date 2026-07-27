# cordglean

*full-fidelity Discord context for agents*

cordglean is a self-hosted Discord MCP server with full-fidelity embeds, channel and thread search, messaging, replies, and reactions.

The current v1 is deliberately read-only. It makes Discord bot messages useful
to MCP clients by returning both a human-readable rendering and the complete
embed structures instead of reducing embed-only messages to empty strings.

## v1 tools

- `read_channel(channel_id, limit=20, before=None, after=None)`
- `get_message(channel_id, message_id, include_raw_embeds=True)`
- `read_thread(thread_id, limit=20, before=None, after=None)`
- `list_channels(guild_id)`
- `read_user_profile(user_id, guild_id=None)`

There are no message, reaction, moderation, role, permission, channel-management,
or webhook mutation tools.

Every returned message has `display_text`, which combines normal message content,
fully rendered embeds, and attachment metadata. `rendered_embeds` preserves:

- title, description, URL, type, color, and timestamp;
- every field name/value and its inline flag;
- footer text and icon metadata;
- author, provider, thumbnail, image, and video metadata.

The raw Discord embed objects are also present in channel/thread results. For a
single message, `include_raw_embeds` controls whether they are included.

## Install and run

Python 3.10 or newer is required. With [`uv`](https://docs.astral.sh/uv/):

```sh
uv sync
export DISCORD_BOT_TOKEN='...'
uv run cordglean
```

Do not put the token in MCP arguments, command-line arguments, source files, or
logs. Inject it through the server process environment. A minimal MCP client
configuration is:

```json
{
  "mcpServers": {
    "cordglean": {
      "command": "uv",
      "args": [
        "--directory",
        "/absolute/path/to/cordglean",
        "run",
        "cordglean"
      ],
      "env": {
        "DISCORD_BOT_TOKEN": "${DISCORD_BOT_TOKEN}"
      }
    }
  }
}
```

The bot needs only Discord permissions appropriate for reading the intended
guild, channels, threads, member profiles, and message history. As optional
defense in depth, `DISCORD_ALLOWED_CHANNEL_IDS` accepts a comma-separated
channel allowlist. Discord's server-side bot permissions remain the primary
boundary.

## Verification

Run the unit suite:

```sh
uv sync --extra dev
uv run pytest
uv run ruff check .
```

The smoke client starts the real stdio server and makes exactly one MCP tool
call:

```sh
uv run python scripts/smoke_mcp.py 1497292797470965943 --limit 5
```

It does not load dotenv files itself. Export `DISCORD_BOT_TOKEN` into the
process environment before running it.

## Deliberate extension points

`ChannelAccessPolicy` is the v1 authorization hook for a future policy richer
than the optional environment allowlist. `DiscordClient` keeps transport and
rendering separate from MCP registration so later services can be introduced
without widening today's tool surface.

Possible v2 work is intentionally not implemented: bounded local
`search_messages`, gated `send_message`, and `add_reaction`. Any future write
surface should live in a separate module and remain opt-in. Deletion, editing
others' messages, moderation, roles, permissions, channel mutation, and webhook
management are outside the project boundary.
