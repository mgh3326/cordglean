package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "1", "yes"} {
		t.Run(flag, func(t *testing.T) {
			values := map[string]string{"DISCORD_BOT_TOKEN": "fake-token", "CORDGLEAN_ENABLE_WRITE": flag, "DISCORD_ALLOWED_CHANNEL_IDS": "123, 456,"}
			cfg, err := loadConfig(func(key string) string { return values[key] })
			if flag == "yes" {
				if err == nil || strings.Contains(err.Error(), "yes") {
					t.Fatal("invalid opt-in must fail closed without echoing the value")
				}
				return
			}
			if err != nil || cfg.enableWrite != (flag == "1") || cfg.apiBaseURL != defaultAPIBaseURL || !cfg.permitsChannel("123") || cfg.permitsChannel("789") {
				t.Fatalf("bad config: %v", err)
			}
		})
	}
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing token accepted")
	}
	for _, bad := range []string{"", "../messages", "１２３", "123\n"} {
		if snowflake(bad, "channel_id") == nil {
			t.Fatalf("bad snowflake accepted: %q", bad)
		}
	}
}

func TestReadValidationBeforeHTTP(t *testing.T) {
	for _, args := range []object{
		{"channel_id": "../messages"}, {"channel_id": "123", "limit": 0}, {"channel_id": "123", "limit": 101},
		{"channel_id": "123", "before": "1", "after": "2"}, {"channel_id": "123", "before": "bad"},
	} {
		session, _, _ := fakeMCP(t, testConfig(), nil)
		if !callTool(t, session, "read_channel", args).IsError {
			t.Fatal("invalid read accepted")
		}
	}
}

func TestChannelAllowlistCoversWritesAndWebhookMetadata(t *testing.T) {
	cfg := testConfig()
	cfg.allowedChannels = map[string]bool{"123": true}
	for _, tc := range []struct {
		tool string
		args object
	}{
		{"read_channel", object{"channel_id": "456"}},
		{"send_message", object{"channel_id": "456", "content": "hello"}},
		{"edit_channel", object{"channel_id": "456", "name": "alerts"}},
		{"create_channel", object{"guild_id": "111", "name": "alerts"}},
		{"archive_channel", object{"channel_id": "123", "archive_category_id": "456"}},
		{"list_webhooks", object{"channel_id": "456"}},
		{"create_webhook", object{"channel_id": "456", "name": "alerts", "output_path": "unused"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			session, _, _ := fakeMCP(t, cfg, nil)
			if !callTool(t, session, tc.tool, tc.args).IsError {
				t.Fatal("allowlist bypass")
			}
		})
	}
	t.Run("delete_webhook_resolves_channel", func(t *testing.T) {
		calls := 0
		session, _, _ := fakeMCP(t, cfg, func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Method != "GET" {
				t.Fatal("unauthorized webhook mutated")
			}
			return fakeResponse(200, object{"id": "789", "channel_id": "456", "name": "alerts"}), nil
		})
		if !callTool(t, session, "delete_webhook", object{"webhook_id": "789", "confirm": "alerts"}).IsError || calls != 1 {
			t.Fatal("webhook allowlist bypass")
		}
	})
	for _, tool := range []string{"list_channels", "list_webhooks"} {
		t.Run(tool, func(t *testing.T) {
			session, _, _ := fakeMCP(t, cfg, func(*http.Request) (*http.Response, error) {
				return fakeResponse(200, []any{object{"id": "123", "channel_id": "123"}, object{"id": "456", "channel_id": "456"}}), nil
			})
			result := callTool(t, session, tool, object{"guild_id": "111"})
			if result.IsError || strings.Contains(resultString(t, result), "456") {
				t.Fatal("allowlist did not filter guild results")
			}
		})
	}
}

func TestReadRateLimitAndSanitizedTransportErrors(t *testing.T) {
	attempts := 0
	session, client, _ := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return fakeResponse(429, object{"retry_after": 0.25}), nil
		}
		return fakeResponse(200, []any{}), nil
	})
	var waits []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil }
	if callTool(t, session, "read_channel", object{"channel_id": "123"}).IsError || attempts != 2 {
		t.Fatal("read retry behavior changed")
	}
	assertJSONEqual(t, waits, []time.Duration{250 * time.Millisecond})
}

func TestRetryAfterParsingAndLongDelayBound(t *testing.T) {
	for _, tc := range []struct {
		header, body string
		want         time.Duration
	}{
		{"", `{"retry_after":0.125}`, 125 * time.Millisecond},
		{"0.25", `{"retry_after":0.5}`, 250 * time.Millisecond},
		{"NaN", `{"retry_after":0.5}`, 500 * time.Millisecond},
		{"", `{"retry_after":"Infinity"}`, time.Second},
		{"-1", `{"retry_after":0.5}`, 500 * time.Millisecond},
		{"-1", `{}`, time.Second}, {"", `{"retry_after":-1}`, time.Second}, {"", `{}`, time.Second},
	} {
		if got := retryAfter(tc.header, []byte(tc.body)); got != tc.want {
			t.Fatalf("retry delay=%v, want=%v", got, tc.want)
		}
	}
	attempts := 0
	session, client, _ := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
		attempts++
		return fakeResponse(429, object{"retry_after": json.Number("60")}), nil
	})
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("oversized delay must not be shortened and retried")
		return nil
	}
	if !callTool(t, session, "send_message", object{"channel_id": "123", "content": "hello"}).IsError || attempts != 1 {
		t.Fatal("long retry delay bound failed")
	}
}
