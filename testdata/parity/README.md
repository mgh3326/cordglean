# Python MCP read parity evidence

These frozen JSON files were captured from the original Python server at commit
`ce7599c026c7159a0677063f98fe24bf48f3ecc1` before its removal. The capture used
the original source retrieved with `git show <commit>:src/cordglean/<module>`;
it did not use the concurrent transport/config changes in the working tree.
`capture.json` records source SHA-256 hashes, dependency versions, observed fake
HTTP requests and hashes of the complete MCP call results.

The harness opened an actual MCP `ClientSession` using
`mcp.shared.memory.create_connected_server_and_client_session` connected to the
original FastMCP server. It called `tools/list` and then `tools/call` for each
case. The only substitution was the server lifespan: it yielded the original
`AppContext` containing the original `DiscordClient` with explicit synthetic
`Settings` and an `httpx.MockTransport`. `Settings.from_environment` was replaced
with an assertion failure so a successful capture could not read Discord
configuration from the process environment. No network sockets, real Discord
requests, real credentials or background processes were used.

For every call, the harness asserted that exactly one fake HTTP request matched
the expected method, path and query, that MCP reported success, and that the JSON
in the text content equaled the MCP `structuredContent`. Each `*.golden.json`
contains that `structuredContent`; key order and whitespace are not contractual.

Files:

- `fixtures.json`: named fake Discord HTTP responses. All identifiers and data
  are synthetic; URLs use the reserved `example.invalid` domain.
- `cases.json`: tool names, arguments, expected requests, fixture keys and golden
  filenames, suitable for the Go parity test to consume directly.
- `tools.json`: the original complete `tools/list` result, including descriptions
  and input/output schemas. Its `list_active_threads` entry is a Go-only read tool
  added in hk 3989; the Python server never had it, so that entry has no Python
  capture. The entry is kept identical to `read_tools.json`, which the server embeds.
- `capture.json`: provenance and observed request evidence.
- Nine goldens: `read_channel.golden.json`,
  `read_channel_before.golden.json`, `read_channel_empty.golden.json`,
  `get_message.golden.json`, `get_message_no_raw.golden.json`,
  `read_thread.golden.json`, `list_channels.golden.json`,
  `read_user_profile.golden.json`, and `read_user_profile_guild.golden.json`.

Coverage includes all five read tools; pagination defaults, cursors and empty
results; omitted raw embeds; user and guild-member profiles; multiple embeds;
Unicode; sparse optional fields; rich author/footer/provider/media metadata;
attachment metadata and unknown raw fields.

Capture command, run with the existing Python environment before deletion:

```sh
uv run --no-sync python <scratch>/capture_python_parity.py
```

Successful output:

```text
PASS MCP read_channel -> read_channel.golden.json
PASS MCP read_channel -> read_channel_before.golden.json
PASS MCP read_channel -> read_channel_empty.golden.json
PASS MCP get_message -> get_message.golden.json
PASS MCP get_message -> get_message_no_raw.golden.json
PASS MCP read_thread -> read_thread.golden.json
PASS MCP list_channels -> list_channels.golden.json
PASS MCP read_user_profile -> read_user_profile.golden.json
PASS MCP read_user_profile -> read_user_profile_guild.golden.json
PASS tools/list: ['read_channel', 'get_message', 'read_thread', 'list_channels', 'read_user_profile']
PASS 9 MCP calls, 9 fake HTTP requests, zero network requests
```

Treat these as Python reference outputs. Do not regenerate them from the Go
implementation when a parity test fails.
