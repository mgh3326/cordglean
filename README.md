# cordglean

*full-fidelity Discord context for agents*

cordglean is a self-hosted Discord MCP server. It runs as one Go binary over
stdio and returns complete message content, embeds, and attachment metadata.
Existing installations stay read-only. Message, channel, and webhook management
tools appear only when `CORDGLEAN_ENABLE_WRITE=1`.

## Build and run

Go 1.25 or newer is required. Build from a checkout:

```sh
go build -o cordglean .
```

Or install the binary with Go:

```sh
go install github.com/mgh3326/cordglean@latest
```

Inject `DISCORD_BOT_TOKEN` through the server process environment, then run
`cordglean`. Keep the token out of arguments, source files, and logs.
cordglean does not load dotenv files.

A minimal MCP client configuration is:

```json
{
	"mcpServers": {
		"cordglean": {
			"command": "/absolute/path/to/cordglean",
			"env": {
				"DISCORD_BOT_TOKEN": "${DISCORD_BOT_TOKEN}"
			}
		}
	}
}
```

To enable management tools for a trusted session, add
`"CORDGLEAN_ENABLE_WRITE": "1"` to that server's `env` object and restart it.
Leave the flag unset or use `0` for read-only access. Other values fail startup.

The Go binary preserves the Python server's read tool names, arguments, defaults,
and JSON output. Update the MCP launch command when replacing a Python install.
The environment names remain the same.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DISCORD_BOT_TOKEN` | Required | Bot authentication for reads and writes |
| `CORDGLEAN_ENABLE_WRITE` | `0` | Register management tools only for `1` |
| `DISCORD_ALLOWED_CHANNEL_IDS` | Unrestricted | Optional comma-separated channel ID allowlist |
| `DISCORD_API_BASE_URL` | `https://discord.com/api/v10` | HTTP API base URL, also useful for test transports |

Discord's bot permissions are the primary access boundary. The optional
allowlist adds a local restriction to channel reads and writes. Guild channel
and webhook lists omit channels outside the allowlist. Webhook deletion checks
the webhook's channel before deleting it. Parent and archive categories must
also be allowlisted. `create_channel` refuses while an ID allowlist is active
because the new channel has no authorized ID yet.

A thread is admitted when its parent channel is allowlisted. The request names
only the thread, so a channel the allowlist does not list is read once with
`GET /channels/{id}`. The answer (the parent of a thread, or none for other
channels) is kept for the life of the process. A failed read refuses the channel
with the same error as an unlisted one. Guild channel listings use the
`parent_id` already in each entry and make no request. Webhook listings by guild
match each webhook's own channel, so webhooks in a thread appear only under a
`channel_id` listing of that thread.

Webhook deletion checks the channel once, then deletes by webhook ID. A move
between those requests is not checked again. To enforce the channel boundary
through Discord, deny the bot Manage Webhooks in channels outside the allowlist.

## Read tools

- `read_channel(channel_id, limit=20, before=null, after=null)`
- `get_message(channel_id, message_id, include_raw_embeds=true)`
- `read_thread(thread_id, limit=20, before=null, after=null)`
- `list_channels(guild_id)`
- `read_user_profile(user_id, guild_id=null)`
- `list_active_threads(channel_id, include_archived=false, limit=50)`

Message results include `display_text`, `rendered_embeds`, and attachment
metadata. Rendered embeds retain titles, descriptions, links, colors,
timestamps, fields, footers, authors, providers, and media metadata. Raw embeds
and attachment objects preserve fields that the renderer does not recognize.
`get_message` can omit raw embeds while keeping the rendered view.

Channel and thread pages contain cursor metadata and are ordered newest first.
Limits range from 1 to 100. `before` and `after` are mutually exclusive.

Read tools need the bot permissions appropriate to the resource, including
View Channel and Read Message History for message history. Discord may require
additional access for voice channels, threads, or guild member profiles.

## Management tools

These nine tools are registered only when `CORDGLEAN_ENABLE_WRITE=1`:

| Tool | Arguments | Bot permissions |
| --- | --- | --- |
| `send_message` | `channel_id`, `content`, optional `embeds`, `allow_mass_mention=false`, `nonce` | Send Messages; Embed Links for embeds; Send Messages in Threads for threads |
| `create_channel` | `guild_id`, `name`, optional `parent_id`, `topic` | Manage Channels |
| `create_thread` | `channel_id`, `name`, optional `content`, `message_id`, `auto_archive_duration` | Create Public Threads; Send Messages for forum posts |
| `edit_channel` | `channel_id`, optional `name`, `topic`, `parent_id` | Manage Channels |
| `archive_channel` | `channel_id`, `archive_category_id` | Manage Channels and Manage Roles |
| `delete_channel` | `channel_id`, `confirm` | Manage Channels |
| `list_webhooks` | Exactly one of `channel_id` or `guild_id` | Manage Webhooks |
| `create_webhook` | `channel_id`, `name`, `output_path`, `overwrite=false` | Manage Webhooks |
| `delete_webhook` | `webhook_id`, `confirm` | Manage Webhooks |

Channel visibility and permission overwrites still apply. A 403 identifies the
permission required by the operation and directs you to check channel access.
Discord's [channel permission rules](https://docs.discord.com/developers/resources/channel#modify-channel)
require Manage Roles when changing permission overwrites.

### Messages

`send_message` returns `message_id` and `nonce`. Content is limited to 2,000 characters.
Messages support up to ten embeds with 6,000 combined text characters, subject
to Discord's per-field limits. Empty content requires an embed.

`@everyone` and `@here` are refused unless `allow_mass_mention=true`, including
occurrences in embeds. The request also sets `allowed_mentions` explicitly.
User and role pings are suppressed; explicit mass-mention opt-in permits only
everyone mentions, subject to the bot's Mention Everyone permission. Limits
follow the [Discord message API](https://docs.discord.com/developers/resources/message#embed-object-embed-limits).

Every message is sent with a Discord `nonce` and `enforce_nonce: true`. The
nonce is the caller's `nonce` (1–25 characters) or a generated 24-character
value. Per the [Create Message](https://docs.discord.com/developers/resources/message#create-message)
API, Discord checks an enforced nonce for uniqueness over the past few minutes.
If the same author already created a message with that nonce, Discord returns
that message and creates no new one. To retry a send whose outcome is unknown,
pass the reported nonce again.

### Threads

`create_thread` opens a thread in one of three ways. A forum or media channel
takes `content`, which becomes the first message of a new post; mentions in that
message are suppressed, as `send_message` suppresses them by default. A text
channel takes `message_id` to start a thread from a message, or neither for a
standalone public thread. `content` is refused for text channels, and
`message_id` for forum channels. `auto_archive_duration` accepts 60, 1440, 4320,
or 10080 minutes. The tool returns `thread_id`, `name`, and `parent_id`.

After an unknown outcome the tool does not retry the request. Check
`list_active_threads` for the name before creating another thread.

`list_active_threads` is a read tool and needs no write flag. It returns the
active threads whose parent is `channel_id`, from the guild's active-thread list.
With `include_archived=true` it adds one page of the channel's archived public
threads, sized by `limit` (1–100, default 50), and returns `has_more`. Each
entry carries `id`, `name`, `parent_id`, `owner_id`, `archived`,
`message_count`, `last_message_id`, and `created`.

### Channel changes and deletion

`create_channel` creates a text channel. `edit_channel` sends only supplied
fields; an empty topic clears it, and an explicit `parent_id=null` removes the
channel from its category. Names have 1–100 characters. Topics have at most
1,024 characters.

`archive_channel` moves a guild channel into a category in the same guild and
denies `@everyone` message sending, thread creation, and sending in threads.
The operation preserves other overwrites and unrelated permission bits. Its
`previous` result contains the old parent and complete overwrites for reversal:
move the channel back and restore those overwrites through Discord's settings
or API. Archiving does not delete messages. Explicit role or member grants and
administrator permissions retain Discord's normal precedence over `@everyone`.

Deletion is permanent. Both delete tools require `confirm` to match the
resource's current name exactly. Missing or empty confirmation makes no HTTP
request. A supplied confirmation triggers one metadata GET; a mismatch sends
no DELETE. A successful match triggers the DELETE.

### Webhook secrets

`list_webhooks` returns only IDs, names, channels, and creator metadata.
`create_webhook` saves the URL to `output_path` and returns only `id`, `name`,
and the absolute `path`. Supply a path in an existing private directory.

Secret files have mode `0600` before any URL is written. An existing destination
is refused before creating the webhook unless `overwrite=true`. Overwrites use
a private temporary file and atomic replacement, preserving the previous file
if creation fails. Symlinks and directories are refused. A failure to save a
created webhook triggers bounded, bot-authenticated cleanup. If cleanup fails,
the error identifies the webhook ID for manual removal.

The exclusive destination reservation prevents concurrent creations from both
using the same path. Hard termination can leave an empty reserved file or an
unfinished remote webhook. Inspect the destination and the channel's webhook
list before retrying. File contents are synced, but directory changes are not;
recovery after power loss is not guaranteed.

Webhook tokens, URLs, response bodies, message content, names, and file paths
are excluded from audit lines. Every management operation that reaches its
handler logs one line with the tool, target ID, and outcome to stderr. The
outcome is `success`, `failure`, `refused`, or `unknown`. The MCP
SDK rejects invalid argument shapes before the handler runs, so those calls
have no operation audit line. Management HTTP and filesystem
errors use fixed descriptions instead of remote response bodies or raw errors.
HTTP requests use only the bot token; webhook URLs are never requested.

### Rate limits

HTTP 429 responses retry at most twice after the first attempt. Retries honor
the `Retry-After` header or JSON `retry_after`, with a one-second fallback for
invalid values. Delays above 30 seconds return a rate-limit error without an
early retry. Transport failures and other HTTP errors are not retried, avoiding
duplicate writes after an ambiguous network failure.

A write (POST, PATCH, PUT, or DELETE) that fails at the transport level, gets
an HTTP 5xx, or gets a successful status with an unreadable or invalid body may
already have taken effect. The tool reports the outcome as unknown, not as a
failure, and the audit line records `unknown`. Read the resource back before
retrying; for `send_message`, check the channel's latest messages, then retry
only with the reported `nonce`. Reads keep their existing errors.

## Verification

Run the Go checks:

```sh
go vet ./...
go test ./...
gofmt -l .
```

The formatting command prints nothing on success. CI runs the same checks.
Tests use fake HTTP transports and in-memory MCP sessions, with no Discord
network calls. [Python parity evidence](testdata/parity/README.md) records nine
original Python MCP results and the complete read tool schemas used to verify
the Go rewrite. Python, pytest, and the previous Ruff linter are no longer
runtime or verification dependencies.
