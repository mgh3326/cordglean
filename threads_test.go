package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// route is one Discord request the fake transport answers. Any request not in the table fails the test.
type route struct {
	method, path string
	status       int
	payload      any
	check        func(*testing.T, *http.Request)
}

func serve(t *testing.T, calls *[]string, table ...route) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		*calls = append(*calls, req.Method+" "+req.URL.String())
		for _, r := range table {
			if req.Method == r.method && req.URL.Path == "/api/v10"+r.path {
				if r.check != nil {
					r.check(t, req)
				}
				return fakeResponse(r.status, r.payload), nil
			}
		}
		t.Errorf("unexpected request %s %s", req.Method, req.URL.String())
		return nil, errors.New("unexpected request")
	}
}

// unlistedChannelOnly answers the one read the allowlist makes for channel 456 (a text channel
// the allowlist does not name) and fails on any other request.
func unlistedChannelOnly(t *testing.T) roundTripFunc {
	var calls []string
	return serve(t, &calls, route{method: "GET", path: "/channels/456", status: 200, payload: object{"id": "456", "type": 0, "guild_id": "111"}})
}

func countCalls(calls []string, want string) int {
	n := 0
	for _, call := range calls {
		if call == want {
			n++
		}
	}
	return n
}

func structuredInto(t *testing.T, result *mcp.CallToolResult, out any) {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func TestCreateThreadRequests(t *testing.T) {
	for _, tc := range []struct {
		name, channel, channelType, path string
		args                             object
		body                             object
	}{
		{
			name: "forum post", channel: "900", channelType: "15", path: "/channels/900/threads",
			args: object{"channel_id": "900", "name": "incident", "content": "hello"},
			body: object{"name": "incident", "message": object{"content": "hello", "allowed_mentions": object{"parse": []string{}}}},
		},
		{
			name: "media post with archive duration", channel: "901", channelType: "16", path: "/channels/901/threads",
			args: object{"channel_id": "901", "name": "incident", "content": "hello", "auto_archive_duration": 1440},
			body: object{"name": "incident", "auto_archive_duration": 1440, "message": object{"content": "hello", "allowed_mentions": object{"parse": []string{}}}},
		},
		{
			name: "text channel from message", channel: "800", channelType: "0", path: "/channels/800/messages/801/threads",
			args: object{"channel_id": "800", "name": "incident", "message_id": "801", "auto_archive_duration": 4320},
			body: object{"name": "incident", "auto_archive_duration": 4320},
		},
		{
			name: "standalone public thread", channel: "800", channelType: "0", path: "/channels/800/threads",
			args: object{"channel_id": "800", "name": "incident"},
			body: object{"name": "incident", "type": 11},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			session, _, logs := fakeMCP(t, testConfig(), serve(t, &calls,
				route{method: "GET", path: "/channels/" + tc.channel, status: 200, payload: object{"id": tc.channel, "type": tc.channelType, "guild_id": "111"}},
				route{method: "POST", path: tc.path, status: 200, payload: object{"id": "901", "name": "incident", "parent_id": tc.channel},
					check: func(t *testing.T, req *http.Request) { assertJSONEqual(t, requestBody(t, req), tc.body) }},
			))
			result := callTool(t, session, "create_thread", tc.args)
			if result.IsError || len(calls) != 2 {
				t.Fatalf("create_thread failed: %+v calls=%v", result, calls)
			}
			assertJSONEqual(t, result.StructuredContent, object{"thread_id": "901", "name": "incident", "parent_id": tc.channel})
			assertOneAudit(t, logs, "create_thread", tc.channel, "success")
		})
	}
}

func TestCreateThreadRefusedBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channel     string
		channelType string
		args        object
	}{
		{"forum without content", "900", "15", object{"channel_id": "900", "name": "incident"}},
		{"forum with empty content", "900", "15", object{"channel_id": "900", "name": "incident", "content": ""}},
		{"forum with message_id", "900", "15", object{"channel_id": "900", "name": "incident", "content": "hi", "message_id": "1"}},
		{"text channel with content", "800", "0", object{"channel_id": "800", "name": "incident", "content": "hi"}},
		{"voice channel", "700", "2", object{"channel_id": "700", "name": "incident"}},
		{"bad duration", "800", "0", object{"channel_id": "800", "name": "incident", "auto_archive_duration": 30}},
		{"empty name", "800", "0", object{"channel_id": "800", "name": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			session, _, logs := fakeMCP(t, testConfig(), serve(t, &calls,
				route{method: "GET", path: "/channels/" + tc.channel, status: 200, payload: object{"id": tc.channel, "type": tc.channelType, "guild_id": "111"}},
			))
			if !callTool(t, session, "create_thread", tc.args).IsError {
				t.Fatal("invalid create_thread accepted")
			}
			for _, call := range calls {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatalf("refused create_thread sent a write: %s", call)
				}
			}
			assertOneAudit(t, logs, "create_thread", tc.channel, "refused")
		})
	}
}

func TestCreateThreadUnknownOutcomeIsNotRetried(t *testing.T) {
	var calls []string
	session, _, logs := fakeMCP(t, testConfig(), serve(t, &calls,
		route{method: "GET", path: "/channels/900", status: 200, payload: object{"id": "900", "type": 15, "guild_id": "111"}},
		route{method: "POST", path: "/channels/900/threads", status: 500, payload: object{"message": "fixture"}},
	))
	result := callTool(t, session, "create_thread", object{"channel_id": "900", "name": "incident", "content": "hi"})
	if !result.IsError || len(calls) != 2 || !strings.HasPrefix(calls[1], "POST ") {
		t.Fatalf("unknown outcome not reported once: calls=%v", calls)
	}
	if !strings.Contains(resultString(t, result), "outcome unknown") {
		t.Fatal("unknown outcome not reported as unknown")
	}
	assertOneAudit(t, logs, "create_thread", "900", "unknown")
}

func TestCreateThreadWriteFlagOff(t *testing.T) {
	cfg := testConfig()
	cfg.enableWrite = false
	session, _, _ := fakeMCP(t, cfg, nil)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_thread", Arguments: object{"channel_id": "900", "name": "incident", "content": "hi"}})
	if err == nil && !result.IsError {
		t.Fatal("create_thread ran without CORDGLEAN_ENABLE_WRITE=1")
	}
}

func TestThreadAdmittedByAllowedParent(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	var calls []string
	session, _, _ := fakeMCP(t, cfg, serve(t, &calls,
		route{method: "GET", path: "/channels/200", status: 200, payload: object{"id": "200", "type": 11, "parent_id": "100", "guild_id": "111"}},
		route{method: "POST", path: "/channels/200/messages", status: 200, payload: object{"id": "301"}},
		route{method: "GET", path: "/channels/200/messages", status: 200, payload: []any{}},
	))
	for i := 0; i < 2; i++ {
		if result := callTool(t, session, "send_message", object{"channel_id": "200", "content": "hi"}); result.IsError {
			t.Fatalf("send to admitted thread refused: %+v", result)
		}
	}
	if result := callTool(t, session, "read_thread", object{"thread_id": "200"}); result.IsError {
		t.Fatalf("read of admitted thread refused: %+v", result)
	}
	if n := countCalls(calls, "GET https://discord.test/api/v10/channels/200"); n != 1 {
		t.Fatalf("parent lookup made %d times, want once: %v", n, calls)
	}
}

func TestThreadRefusedWithoutAllowedParent(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	for _, tc := range []struct {
		name, channel, payload string
		tool                   string
		args                   object
	}{
		{"thread under unlisted parent write", "300", `{"id":"300","type":11,"parent_id":"999","guild_id":"111"}`, "send_message", object{"channel_id": "300", "content": "hi"}},
		{"thread under unlisted parent read", "300", `{"id":"300","type":11,"parent_id":"999","guild_id":"111"}`, "read_channel", object{"channel_id": "300"}},
		{"text channel not listed", "400", `{"id":"400","type":0,"guild_id":"111"}`, "send_message", object{"channel_id": "400", "content": "hi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			var payload any
			if err := json.Unmarshal([]byte(tc.payload), &payload); err != nil {
				t.Fatal(err)
			}
			session, _, _ := fakeMCP(t, cfg, serve(t, &calls,
				route{method: "GET", path: "/channels/" + tc.channel, status: 200, payload: payload},
			))
			if !callTool(t, session, tc.tool, tc.args).IsError {
				t.Fatal("channel outside the allowlist accepted")
			}
			for _, call := range calls {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatalf("refused channel reached a write: %s", call)
				}
			}
		})
	}
}

func TestThreadParentLookupFailureRefuses(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	var calls []string
	session, _, _ := fakeMCP(t, cfg, serve(t, &calls,
		route{method: "GET", path: "/channels/500", status: 500, payload: object{"message": "fixture"}},
	))
	for i := 0; i < 2; i++ {
		result := callTool(t, session, "send_message", object{"channel_id": "500", "content": "hi"})
		if !result.IsError || !strings.Contains(resultString(t, result), errChannelNotAllowed) {
			t.Fatalf("failed parent lookup did not refuse with the allowlist error: %+v", result)
		}
	}
	// A failed lookup is not cached, so each attempt asks Discord again.
	if n := countCalls(calls, "GET https://discord.test/api/v10/channels/500"); n != 2 {
		t.Fatalf("failed lookup made %d requests, want 2: %v", n, calls)
	}
}

func TestListChannelsAdmitsThreadsOfAllowedParents(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	session, _, _ := fakeMCP(t, cfg, func(*http.Request) (*http.Response, error) {
		return fakeResponse(200, []any{
			object{"id": "100", "type": 0, "parent_id": nil},
			object{"id": "200", "type": 11, "parent_id": "100"},
			object{"id": "300", "type": 11, "parent_id": "999"},
			object{"id": "400", "type": 0, "parent_id": nil},
		}), nil
	})
	result := callTool(t, session, "list_channels", object{"guild_id": "111"})
	var out struct {
		Channels []struct {
			ID string `json:"id"`
		} `json:"channels"`
	}
	structuredInto(t, result, &out)
	var ids []string
	for _, c := range out.Channels {
		ids = append(ids, c.ID)
	}
	assertJSONEqual(t, ids, []string{"100", "200"})
}

func TestThreadWebhooksListedUnderAllowedParent(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	var calls []string
	session, _, _ := fakeMCP(t, cfg, serve(t, &calls,
		route{method: "GET", path: "/channels/200", status: 200, payload: object{"id": "200", "type": 11, "parent_id": "100", "guild_id": "111"}},
		route{method: "GET", path: "/channels/200/webhooks", status: 200, payload: []any{
			object{"id": "700", "name": "alerts", "channel_id": "200", "token": fixtureSecret},
		}},
	))
	result := callTool(t, session, "list_webhooks", object{"channel_id": "200"})
	if result.IsError {
		t.Fatalf("thread webhooks refused: %+v", result)
	}
	assertJSONEqual(t, result.StructuredContent, object{"webhook_count": 1, "webhooks": []any{object{"id": "700", "name": "alerts", "channel_id": "200", "creator": nil}}})
	if strings.Contains(resultString(t, result), fixtureSecret) {
		t.Fatal("webhook list leaked secret")
	}
}

func TestListActiveThreads(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"100": true}
	activeThread := func(id, parent string) object {
		return object{"id": id, "name": "t" + id, "parent_id": parent, "owner_id": "7", "message_count": 3, "last_message_id": "900",
			"thread_metadata": object{"archived": false, "create_timestamp": "2026-11-01T00:00:00+00:00"}}
	}
	active := route{method: "GET", path: "/guilds/111/threads/active", status: 200, payload: object{
		"threads": []any{activeThread("201", "100"), activeThread("202", "999")}, "members": []any{}, "has_more": false,
	}}
	archived := route{method: "GET", path: "/channels/100/threads/archived/public", status: 200,
		payload: object{"threads": []any{object{"id": "203", "name": "t203", "parent_id": "100", "thread_metadata": object{"archived": true}}}, "has_more": true},
		check: func(t *testing.T, req *http.Request) {
			if req.URL.Query().Get("limit") != "50" {
				t.Errorf("archived page limit=%q, want 50", req.URL.Query().Get("limit"))
			}
		}}
	channel := route{method: "GET", path: "/channels/100", status: 200, payload: object{"id": "100", "type": 0, "guild_id": "111"}}

	t.Run("active only", func(t *testing.T) {
		var calls []string
		session, _, _ := fakeMCP(t, cfg, serve(t, &calls, channel, active))
		result := callTool(t, session, "list_active_threads", object{"channel_id": "100"})
		if result.IsError {
			t.Fatalf("list failed: %+v", result)
		}
		assertJSONEqual(t, result.StructuredContent, object{
			"channel_id": "100", "include_archived": false, "thread_count": 1,
			"threads": []any{object{"id": "201", "name": "t201", "parent_id": "100", "owner_id": "7", "archived": false,
				"message_count": 3, "last_message_id": "900", "created": "2026-11-01T00:00:00+00:00"}},
		})
		if countCalls(calls, "GET https://discord.test/api/v10/channels/100/threads/archived/public?limit=50") != 0 {
			t.Fatal("archived page requested without include_archived")
		}
	})

	t.Run("with one archived page", func(t *testing.T) {
		var calls []string
		session, _, _ := fakeMCP(t, cfg, serve(t, &calls, channel, active, archived))
		result := callTool(t, session, "list_active_threads", object{"channel_id": "100", "include_archived": true, "limit": 50})
		if result.IsError {
			t.Fatalf("list failed: %+v", result)
		}
		var out struct {
			ThreadCount int  `json:"thread_count"`
			HasMore     bool `json:"has_more"`
			Threads     []struct {
				ID       string `json:"id"`
				Archived bool   `json:"archived"`
			} `json:"threads"`
		}
		structuredInto(t, result, &out)
		if out.ThreadCount != 2 || !out.HasMore || out.Threads[0].ID != "201" || out.Threads[1].ID != "203" || !out.Threads[1].Archived {
			t.Fatalf("unexpected listing: %+v", out)
		}
		if n := countCalls(calls, "GET https://discord.test/api/v10/channels/100/threads/archived/public?limit=50"); n != 1 {
			t.Fatalf("archived page requested %d times, want once", n)
		}
	})

	t.Run("refused for an unlisted channel", func(t *testing.T) {
		session, _, _ := fakeMCP(t, cfg, unlistedChannelOnly(t))
		if !callTool(t, session, "list_active_threads", object{"channel_id": "456"}).IsError {
			t.Fatal("unlisted channel listed threads")
		}
	})

	t.Run("limit bounds", func(t *testing.T) {
		var calls []string
		session, _, _ := fakeMCP(t, cfg, serve(t, &calls, channel))
		if !callTool(t, session, "list_active_threads", object{"channel_id": "100", "limit": 0}).IsError {
			t.Fatal("limit 0 accepted")
		}
	})
}
