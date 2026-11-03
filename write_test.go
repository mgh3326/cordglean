package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtureSecret = "FAKE_WEBHOOK_SECRET_MARKER_DO_NOT_USE"

func fakeMCP(t *testing.T, cfg config, handler roundTripFunc) (*mcp.ClientSession, *discordClient, *bytes.Buffer) {
	t.Helper()
	if handler == nil {
		handler = func(*http.Request) (*http.Response, error) {
			t.Error("unexpected HTTP request")
			return nil, errors.New("unexpected fake request")
		}
	}
	c := newDiscordClient(cfg, &http.Client{Transport: handler})
	logs := new(bytes.Buffer)
	return connectMCP(t, newServer(c, log.New(logs, "", 0))), c, logs
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args object) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requestBody(t *testing.T, req *http.Request) object {
	t.Helper()
	var body object
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func resultString(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertOneAudit(t *testing.T, logs *bytes.Buffer, tool, target, outcome string) {
	t.Helper()
	want := "tool=" + tool + " target=" + target + " outcome=" + outcome + "\n"
	if logs.String() != want {
		t.Fatalf("audit mismatch: got %q, want %q", logs.String(), want)
	}
}

func TestWriteFlagRegistration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			cfg := testConfig()
			cfg.enableWrite = enabled
			session, _, _ := fakeMCP(t, cfg, nil)
			list, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{"read_channel": true, "read_thread": true, "get_message": true, "list_channels": true, "read_user_profile": true}
			if enabled {
				for _, name := range []string{"send_message", "create_channel", "edit_channel", "archive_channel", "delete_channel", "list_webhooks", "create_webhook", "delete_webhook"} {
					want[name] = true
				}
			}
			got := make(map[string]bool)
			for _, tool := range list.Tools {
				got[tool.Name] = true
			}
			assertJSONEqual(t, got, want)
			if !enabled {
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "send_message", Arguments: object{"channel_id": "123", "content": "hello"}})
				if err == nil && !result.IsError {
					t.Fatal("disabled write tool accepted a call")
				}
			}
		})
	}
}

func TestWriteRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		args, body         object
	}{
		{"send_message", "POST", "/channels/123/messages", object{"channel_id": "123", "content": "hello", "embeds": []any{object{"description": "status"}}, "nonce": "retry-1"}, object{"content": "hello", "embeds": []any{object{"description": "status"}}, "allowed_mentions": object{"parse": []string{}}, "nonce": "retry-1", "enforce_nonce": true}},
		{"create_channel", "POST", "/guilds/111/channels", object{"guild_id": "111", "name": "alerts", "parent_id": "456", "topic": "monitoring"}, object{"name": "alerts", "type": 0, "parent_id": "456", "topic": "monitoring"}},
		{"edit_channel", "PATCH", "/channels/123", object{"channel_id": "123", "name": "new-alerts", "parent_id": "456", "topic": ""}, object{"name": "new-alerts", "parent_id": "456", "topic": ""}},
		{"edit_channel", "PATCH", "/channels/123", object{"channel_id": "123", "parent_id": nil}, object{"parent_id": nil}},
	} {
		t.Run(tc.name+tc.method+text(tc.args["parent_id"]), func(t *testing.T) {
			calls := 0
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != tc.method || req.URL.Path != "/api/v10"+tc.path || req.Header.Get("Authorization") != "Bot fake-bot-token" {
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				}
				assertJSONEqual(t, requestBody(t, req), tc.body)
				return fakeResponse(200, object{"id": "789", "name": "alerts"}), nil
			})
			result := callTool(t, session, tc.name, tc.args)
			if result.IsError || calls != 1 {
				t.Fatalf("write failed: %+v, calls=%d", result, calls)
			}
			target := "123"
			if tc.name == "create_channel" {
				target = "111"
			}
			assertOneAudit(t, logs, tc.name, target, "success")
		})
	}
}

func TestArchivePreservesPermissionsAndReversalState(t *testing.T) {
	for _, hasEveryone := range []bool{false, true} {
		t.Run(strconv.FormatBool(hasEveryone), func(t *testing.T) {
			original := []any{object{"id": "500", "type": 0, "allow": "4096", "deny": "0"}}
			if hasEveryone {
				original = append(original, object{"id": "111", "type": 0, "allow": "3072", "deny": "16"})
			}
			calls := 0
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				calls++
				switch calls {
				case 1:
					if req.Method != "GET" || req.URL.Path != "/api/v10/channels/123" {
						t.Fatal("expected source channel lookup")
					}
					return fakeResponse(200, object{"id": "123", "guild_id": "111", "type": 0, "parent_id": "321", "permission_overwrites": original}), nil
				case 2:
					if req.Method != "GET" || req.URL.Path != "/api/v10/channels/456" {
						t.Fatal("expected destination lookup")
					}
					return fakeResponse(200, object{"id": "456", "guild_id": "111", "type": 4}), nil
				case 3:
					if req.Method != "PATCH" || req.URL.Path != "/api/v10/channels/123" {
						t.Fatal("expected single archive PATCH")
					}
					allow, deny := "0", archiveWriteBits
					if hasEveryone {
						allow, deny = "1024", deny|16
					}
					assertJSONEqual(t, requestBody(t, req), object{"parent_id": "456", "permission_overwrites": []any{original[0], object{"id": "111", "type": 0, "allow": allow, "deny": strconv.FormatUint(deny, 10)}}})
					return fakeResponse(200, object{"id": "123"}), nil
				default:
					t.Fatal("unexpected extra request")
					return nil, errors.New("unexpected request")
				}
			})
			result := callTool(t, session, "archive_channel", object{"channel_id": "123", "archive_category_id": "456"})
			if result.IsError || calls != 3 {
				t.Fatalf("archive failed: %+v", result)
			}
			assertJSONEqual(t, result.StructuredContent, object{"channel_id": "123", "parent_id": "456", "previous": object{"parent_id": "321", "permission_overwrites": original}})
			assertOneAudit(t, logs, "archive_channel", "123", "success")
		})
	}
}

func TestDeletionConfirmations(t *testing.T) {
	for _, tool := range []string{"delete_channel", "delete_webhook"} {
		for _, confirm := range []any{nil, "", "wrong", "current-name"} {
			t.Run(tool+text(confirm), func(t *testing.T) {
				calls, deletes := 0, 0
				resource, key, id := "channels", "channel_id", "123"
				if tool == "delete_webhook" {
					resource, key, id = "webhooks", "webhook_id", "789"
				}
				session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
					calls++
					if req.URL.Path != "/api/v10/"+resource+"/"+id {
						t.Fatal("wrong deletion route")
					}
					if req.Method == "GET" {
						return fakeResponse(200, object{"id": id, "channel_id": "123", "name": "current-name", "token": fixtureSecret}), nil
					}
					if req.Method != "DELETE" {
						t.Fatal("unexpected method")
					}
					deletes++
					return fakeResponse(204, nil), nil
				})
				args := object{key: id}
				if confirm != nil {
					args["confirm"] = confirm
				}
				result := callTool(t, session, tool, args)
				wantCalls, wantDeletes, outcome := 0, 0, "refused"
				if confirm == "wrong" {
					wantCalls = 1
				}
				if confirm == "current-name" {
					wantCalls, wantDeletes, outcome = 2, 1, "success"
				}
				if calls != wantCalls || deletes != wantDeletes || result.IsError != (outcome == "refused") {
					t.Fatalf("confirmation failed: calls=%d deletes=%d result=%+v", calls, deletes, result)
				}
				assertOneAudit(t, logs, tool, id, outcome)
				if strings.Contains(resultString(t, result)+logs.String(), fixtureSecret) {
					t.Fatal("deletion leaked webhook secret")
				}
			})
		}
	}
}

func TestWebhookListsExcludeSecrets(t *testing.T) {
	for _, scope := range []string{"channel_id", "guild_id"} {
		t.Run(scope, func(t *testing.T) {
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				path := "/api/v10/channels/123/webhooks"
				if scope == "guild_id" {
					path = "/api/v10/guilds/123/webhooks"
				}
				if req.Method != "GET" || req.URL.Path != path {
					t.Fatal("unexpected webhook listing route")
				}
				return fakeResponse(200, []any{object{"id": "789", "name": "alerts", "channel_id": "123", "token": fixtureSecret, "url": fixtureSecret, "user": object{"id": "42", "username": "bot", "token": fixtureSecret, "url": fixtureSecret}}}), nil
			})
			result := callTool(t, session, "list_webhooks", object{scope: "123"})
			if result.IsError {
				t.Fatalf("list failed: %+v", result)
			}
			assertJSONEqual(t, result.StructuredContent, object{"webhook_count": 1, "webhooks": []any{object{"id": "789", "name": "alerts", "channel_id": "123", "creator": object{"id": "42", "username": "bot", "global_name": nil}}}})
			if strings.Contains(resultString(t, result)+logs.String(), fixtureSecret) {
				t.Fatal("webhook list leaked secret")
			}
			assertOneAudit(t, logs, "list_webhooks", "123", "success")
		})
	}
}

func TestWebhookPrivateFileAndOverwrite(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(strconv.FormatBool(overwrite), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "alerts.url")
			if overwrite {
				if err := os.WriteFile(path, []byte("previous"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "POST" || req.URL.Path != "/api/v10/channels/123/webhooks" {
					t.Fatal("unexpected create webhook request")
				}
				assertJSONEqual(t, requestBody(t, req), object{"name": "alerts"})
				if overwrite {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "previous" {
						t.Fatal("old destination was changed before webhook creation succeeded")
					}
				}
				return fakeResponse(200, object{"id": "789", "name": "alerts", "token": fixtureSecret, "url": fixtureSecret}), nil
			})
			result := callTool(t, session, "create_webhook", object{"channel_id": "123", "name": "alerts", "output_path": path, "overwrite": overwrite})
			if result.IsError || calls != 1 {
				t.Fatalf("create failed: %+v", result)
			}
			assertJSONEqual(t, result.StructuredContent, object{"id": "789", "name": "alerts", "path": path})
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "https://discord.com/api/webhooks/789/"+fixtureSecret+"\n" {
				t.Fatal("private webhook URL was not saved")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("wrong secret file mode: %v / %v", info, err)
			}
			if strings.Contains(resultString(t, result)+logs.String(), fixtureSecret) || strings.Contains(resultString(t, result), "api/webhooks") {
				t.Fatal("webhook creation leaked URL")
			}
			assertOneAudit(t, logs, "create_webhook", "123", "success")
			logs.Reset()
			refused := callTool(t, session, "create_webhook", object{"channel_id": "123", "name": "alerts", "output_path": path})
			if !refused.IsError || calls != 1 {
				t.Fatal("existing output_path was not refused before HTTP")
			}
			assertOneAudit(t, logs, "create_webhook", "123", "refused")
		})
	}
}

func TestWebhookSymlinksAreRefused(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(strconv.FormatBool(overwrite), func(t *testing.T) {
			dir := t.TempDir()
			target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
			if err := os.WriteFile(target, []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			session, _, _ := fakeMCP(t, testConfig(), nil)
			result := callTool(t, session, "create_webhook", object{"channel_id": "123", "name": "alerts", "output_path": link, "overwrite": overwrite})
			if !result.IsError {
				t.Fatal("symlink accepted")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "unchanged" {
				t.Fatal("symlink target was changed")
			}
		})
	}
}

func TestMessageRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args object
	}{
		{"everyone", object{"content": "hi @everyone"}},
		{"here", object{"content": "hi @here"}},
		{"over_limit", object{"content": strings.Repeat("x", 2001)}},
		{"empty", object{"content": ""}},
		{"embed_mention", object{"content": "", "embeds": []any{object{"description": "@everyone"}}}},
		{"too_many_embeds", object{"content": "", "embeds": make([]object, 11)}},
		{"embed_description", object{"content": "", "embeds": []any{object{"description": strings.Repeat("x", 4097)}}}},
		{"embed_total", object{"content": "", "embeds": []any{object{"description": strings.Repeat("x", 3001)}, object{"description": strings.Repeat("x", 3000)}}}},
		{"embed_field", object{"content": "", "embeds": []any{object{"fields": []any{object{"name": "field", "value": strings.Repeat("x", 1025)}}}}}},
		{"nonce_too_long", object{"content": "hi", "nonce": strings.Repeat("n", 26)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, _, _ := fakeMCP(t, testConfig(), nil)
			tc.args["channel_id"] = "123"
			if !callTool(t, session, "send_message", tc.args).IsError {
				t.Fatal("unsafe message accepted")
			}
		})
	}
}

func TestMessageBoundariesAndExplicitMassMention(t *testing.T) {
	for _, content := range []string{strings.Repeat("x", 2000), "hello @everyone @here"} {
		session, _, _ := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
			assertJSONEqual(t, requestBody(t, req)["allowed_mentions"], object{"parse": []string{"everyone"}})
			return fakeResponse(200, object{"id": "789"}), nil
		})
		result := callTool(t, session, "send_message", object{"channel_id": "123", "content": content, "allow_mass_mention": true})
		if result.IsError {
			t.Fatal("valid boundary/explicit mention refused")
		}
	}
}

func TestMissingEmbedTextIsRefusedBeforeHTTP(t *testing.T) {
	for _, embed := range []object{
		{"fields": []any{object{}}},
		{"fields": []any{object{"name": "field"}}},
		{"fields": []any{object{"value": "value"}}},
		{"footer": object{}},
		{"author": object{}},
	} {
		requests := 0
		session, _, _ := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
			requests++
			return fakeResponse(200, object{"id": "789"}), nil
		})
		result := callTool(t, session, "send_message", object{"channel_id": "123", "content": "", "embeds": []any{embed}})
		if requests != 0 {
			t.Fatalf("invalid embed sent %d HTTP request(s); want 0", requests)
		}
		if !result.IsError {
			t.Fatal("missing required embed text was accepted")
		}
	}
}

func TestWriteRateLimitRetryBound(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(strconv.FormatBool(exhausted), func(t *testing.T) {
			attempts := 0
			session, client, logs := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
				attempts++
				if exhausted || attempts == 1 {
					resp := fakeResponse(429, object{"retry_after": 0.5, "message": fixtureSecret})
					resp.Header.Set("Retry-After", "0.25")
					return resp, nil
				}
				return fakeResponse(200, object{"id": "789"}), nil
			})
			var waits []time.Duration
			client.sleep = func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil }
			result := callTool(t, session, "send_message", object{"channel_id": "123", "content": "hello"})
			wantAttempts, outcome := 2, "success"
			wantWaits := []time.Duration{250 * time.Millisecond}
			if exhausted {
				wantAttempts, outcome = maxRateLimitRetries+1, "failure"
				wantWaits = append(wantWaits, 250*time.Millisecond)
			}
			if attempts != wantAttempts || result.IsError != exhausted {
				t.Fatalf("wrong retry bound: attempts=%d result=%+v", attempts, result)
			}
			assertJSONEqual(t, waits, wantWaits)
			assertOneAudit(t, logs, "send_message", "123", outcome)
			if strings.Contains(resultString(t, result)+logs.String(), fixtureSecret) {
				t.Fatal("rate limit leaked response body")
			}
		})
	}
}

func TestForbiddenNamesRequiredPermissionWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		tool, permission string
		args             object
	}{
		{"send_message", "Send Messages", object{"channel_id": "123", "content": "private content"}},
		{"create_channel", "Manage Channels", object{"guild_id": "111", "name": "alerts"}},
		{"edit_channel", "Manage Channels", object{"channel_id": "123", "name": "alerts"}},
		{"archive_channel", "Manage Channels and Manage Roles", object{"channel_id": "123", "archive_category_id": "456"}},
		{"delete_channel", "Manage Channels", object{"channel_id": "123", "confirm": "alerts"}},
		{"list_webhooks", "Manage Webhooks", object{"channel_id": "123"}},
		{"create_webhook", "Manage Webhooks", object{"channel_id": "123", "name": "alerts", "output_path": filepath.Join(t.TempDir(), "secret.url")}},
		{"delete_webhook", "Manage Webhooks", object{"webhook_id": "789", "confirm": "alerts"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			session, _, logs := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
				return fakeResponse(403, object{"message": fixtureSecret, "url": fixtureSecret, "token": fixtureSecret}), nil
			})
			result := callTool(t, session, tc.tool, tc.args)
			output := resultString(t, result)
			if !result.IsError || !strings.Contains(output, tc.permission) || strings.Contains(output+logs.String(), fixtureSecret) {
				t.Fatalf("unsafe or unhelpful permission error: %s", output)
			}
			if strings.Count(logs.String(), "\n") != 1 || strings.Contains(logs.String(), "private content") {
				t.Fatal("write did not produce exactly one safe audit line")
			}
		})
	}
}

func TestWebhookErrorPathsDoNotLeakSecrets(t *testing.T) {
	for _, kind := range []string{"http", "transport", "invalid_json", "invalid_token", "save_failure"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret.url")
			deletes := 0
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				if req.Method == "DELETE" {
					deletes++
					if req.URL.Path != "/api/v10/webhooks/789" {
						t.Fatal("cleanup must use bot auth, never webhook token")
					}
					return fakeResponse(204, nil), nil
				}
				switch kind {
				case "http":
					return fakeResponse(400, object{"message": fixtureSecret}), nil
				case "transport":
					return nil, errors.New(fixtureSecret)
				case "invalid_json":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fixtureSecret))}, nil
				case "invalid_token":
					return fakeResponse(200, object{"id": "789", "token": fixtureSecret + "/invalid"}), nil
				case "save_failure":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				return fakeResponse(200, object{"id": "789", "token": fixtureSecret}), nil
			})
			result := callTool(t, session, "create_webhook", object{"channel_id": "123", "name": "alerts", "output_path": path})
			if !result.IsError || strings.Contains(resultString(t, result)+logs.String(), fixtureSecret) {
				t.Fatal("webhook error leaked secret or returned success")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("failed creation left a partial secret file")
			}
			wantDeletes := 0
			if kind == "invalid_token" || kind == "save_failure" {
				wantDeletes = 1
			}
			if deletes != wantDeletes {
				t.Fatalf("cleanup calls=%d, want=%d", deletes, wantDeletes)
			}
			outcome := "failure"
			if kind == "transport" || kind == "invalid_json" {
				// The webhook may exist even though its secret was not returned.
				outcome = "unknown"
			}
			assertOneAudit(t, logs, "create_webhook", "123", outcome)
		})
	}
}

func TestAmbiguousWriteOutcomeIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func() (*http.Response, error)
		outcome string
	}{
		{"transport", func() (*http.Response, error) { return nil, errors.New("timeout after send") }, "unknown"},
		{"http_500", func() (*http.Response, error) { return fakeResponse(500, object{"message": fixtureSecret}), nil }, "unknown"},
		{"http_503", func() (*http.Response, error) { return fakeResponse(503, nil), nil }, "unknown"},
		{"http_400", func() (*http.Response, error) { return fakeResponse(400, object{"message": fixtureSecret}), nil }, "failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var nonce any
			calls := 0
			session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
				calls++
				body := requestBody(t, req)
				nonce = body["nonce"]
				if body["enforce_nonce"] != true {
					t.Fatalf("enforce_nonce missing: %v", body)
				}
				return tc.respond()
			})
			result := callTool(t, session, "send_message", object{"channel_id": "123", "content": "hello"})
			output := resultString(t, result)
			if !result.IsError || calls != 1 || strings.Contains(output+logs.String(), fixtureSecret) {
				t.Fatalf("calls=%d result=%s", calls, output)
			}
			generated, _ := nonce.(string)
			if len(generated) != 24 {
				t.Fatalf("generated nonce %q, want 24 characters", generated)
			}
			unknown := strings.Contains(output, "outcome unknown") && strings.Contains(output, "read_channel") &&
				strings.Contains(output, "nonce="+generated)
			if unknown != (tc.outcome == "unknown") {
				t.Fatalf("outcome hint mismatch for %s: %s", tc.outcome, output)
			}
			assertOneAudit(t, logs, "send_message", "123", tc.outcome)
		})
	}
	t.Run("other_writes", func(t *testing.T) {
		for _, tc := range []struct {
			tool, target string
			args         object
		}{
			{"create_channel", "111", object{"guild_id": "111", "name": "alerts"}},
			{"edit_channel", "123", object{"channel_id": "123", "name": "alerts"}},
		} {
			session, _, logs := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
				return nil, errors.New("timeout after send")
			})
			result := callTool(t, session, tc.tool, tc.args)
			if output := resultString(t, result); !result.IsError || !strings.Contains(output, "read the resource back before retrying") {
				t.Fatalf("%s: %s", tc.tool, output)
			}
			assertOneAudit(t, logs, tc.tool, tc.target, "unknown")
		}
	})
	t.Run("reads_unchanged", func(t *testing.T) {
		session, _, _ := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
			return nil, errors.New("timeout")
		})
		output := resultString(t, callTool(t, session, "read_channel", object{"channel_id": "123"}))
		if !strings.Contains(output, "Discord API request failed") || strings.Contains(output, "unknown") {
			t.Fatalf("read error changed: %s", output)
		}
	})
}

func TestSendMessageNonceRetry(t *testing.T) {
	var nonces []any
	session, _, logs := fakeMCP(t, testConfig(), func(req *http.Request) (*http.Response, error) {
		nonces = append(nonces, requestBody(t, req)["nonce"])
		if len(nonces) == 1 {
			return nil, errors.New("timeout after send")
		}
		// Discord answers a repeated enforced nonce with the original message.
		return fakeResponse(200, object{"id": "789"}), nil
	})
	first := callTool(t, session, "send_message", object{"channel_id": "123", "content": "hello"})
	nonce, _ := nonces[0].(string)
	if !first.IsError || nonce == "" || !strings.Contains(resultString(t, first), "nonce="+nonce) {
		t.Fatalf("first attempt did not report its nonce: %s", resultString(t, first))
	}
	retry := callTool(t, session, "send_message", object{"channel_id": "123", "content": "hello", "nonce": nonce})
	if retry.IsError || nonces[1] != nonce {
		t.Fatalf("retry did not reuse nonce: %v %s", nonces, resultString(t, retry))
	}
	assertJSONEqual(t, retry.StructuredContent, object{"message_id": "789", "nonce": nonce})
	if logs.String() != "tool=send_message target=123 outcome=unknown\ntool=send_message target=123 outcome=success\n" {
		t.Fatalf("audit mismatch: %q", logs.String())
	}
}
