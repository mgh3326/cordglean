package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func fakeResponse(status int, payload any) *http.Response {
	data, _ := json.Marshal(payload)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}
}

func testConfig() config {
	return config{token: "fake-bot-token", apiBaseURL: "https://discord.test/api/v10", enableWrite: true}
}

func connectMCP(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "parity-test", Version: "1"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	normalize := func(value any) any {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var result any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !reflect.DeepEqual(normalize(got), normalize(want)) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("JSON mismatch\ngot: %s\nwant: %s", g, w)
	}
}

func TestPythonReadParity(t *testing.T) {
	var cases []struct {
		Name, Tool, Fixture, Golden string
		Arguments                   object
		Request                     struct {
			Method, Path string
			Query        map[string]string
		}
	}
	readJSON(t, "testdata/parity/cases.json", &cases)
	var fixtures map[string]json.RawMessage
	readJSON(t, "testdata/parity/fixtures.json", &fixtures)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			calls := 0
			h := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != tc.Request.Method || req.URL.Path != tc.Request.Path {
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
				}
				query := make(map[string]string)
				for key := range req.URL.Query() {
					query[key] = req.URL.Query().Get(key)
				}
				assertJSONEqual(t, query, tc.Request.Query)
				if req.Header.Get("Authorization") != "Bot fake-bot-token" {
					t.Fatal("missing bot authorization")
				}
				return fakeResponse(200, fixtures[tc.Fixture]), nil
			})}
			client := newDiscordClient(testConfig(), h)
			session := connectMCP(t, newServer(client, nil))
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.Tool, Arguments: tc.Arguments})
			if err != nil || result.IsError {
				t.Fatalf("call failed: %v / %+v", err, result)
			}
			var golden json.RawMessage
			readJSON(t, filepath.Join("testdata/parity", tc.Golden), &golden)
			assertJSONEqual(t, result.StructuredContent, golden)
			if len(result.Content) != 1 {
				t.Fatalf("expected one JSON text content block, got %d", len(result.Content))
			}
			content, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("unexpected MCP content %T", result.Content[0])
			}
			assertJSONEqual(t, json.RawMessage(content.Text), golden)
			if calls != 1 {
				t.Fatalf("expected one fake request, got %d", calls)
			}
		})
	}
}

func TestPythonToolListParity(t *testing.T) {
	client := newDiscordClient(testConfig(), nil)
	client.config.enableWrite = false
	session := connectMCP(t, newServer(client, nil))
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Tools []*mcp.Tool `json:"tools"`
	}
	readJSON(t, "testdata/parity/tools.json", &reference)
	assertJSONEqual(t, result.Tools, reference.Tools)
}

func TestReadRawNumbersRemainExact(t *testing.T) {
	const large = "9007199254740993"
	session, _, _ := fakeMCP(t, testConfig(), func(*http.Request) (*http.Response, error) {
		return fakeResponse(200, object{
			"id": "789", "embeds": []any{object{"description": "visible", "future_value": json.Number(large)}},
			"attachments": []any{object{"id": "42", "future_value": json.Number(large)}},
		}), nil
	})
	result := callTool(t, session, "get_message", object{"channel_id": "123", "message_id": "789"})
	if result.IsError || len(result.Content) != 1 {
		t.Fatal("raw-number read failed")
	}
	// The SDK client decodes structuredContent through float64 too. Check the
	// JSON text emitted by the server, before any client-side numeric conversion.
	content := result.Content[0].(*mcp.TextContent).Text
	if strings.Count(content, large) != 2 || strings.Contains(content, "9007199254740992") {
		t.Fatal("raw embed or attachment number was rounded on the server")
	}
}

func TestInvalidToolListCursorIsRejected(t *testing.T) {
	session, _, _ := fakeMCP(t, testConfig(), nil)
	if _, err := session.ListTools(context.Background(), &mcp.ListToolsParams{Cursor: "invalid-cursor"}); err == nil {
		t.Fatal("invalid tool cursor was accepted")
	}
	if result, err := session.ListTools(context.Background(), nil); err != nil || len(result.Tools) != 15 {
		t.Fatal("server did not remain usable after rejecting the cursor")
	}
}
