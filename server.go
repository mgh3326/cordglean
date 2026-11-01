package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed read_tools.json
var readToolsJSON []byte

type channelReadInput struct {
	ChannelID string  `json:"channel_id"`
	Limit     int     `json:"limit"`
	Before    *string `json:"before"`
	After     *string `json:"after"`
}

type threadReadInput struct {
	ThreadID string  `json:"thread_id"`
	Limit    int     `json:"limit"`
	Before   *string `json:"before"`
	After    *string `json:"after"`
}

type messageReadInput struct {
	ChannelID        string `json:"channel_id"`
	MessageID        string `json:"message_id"`
	IncludeRawEmbeds bool   `json:"include_raw_embeds"`
}

type guildInput struct {
	GuildID string `json:"guild_id"`
}

type userReadInput struct {
	UserID  string  `json:"user_id"`
	GuildID *string `json:"guild_id"`
}

func addReadTool[In any](s *mcp.Server, tool *mcp.Tool, fn func(context.Context, In) (object, error)) {
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := fn(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(out)
		if err != nil {
			return nil, nil, errors.New("read result could not be encoded")
		}
		// SDK output-schema normalization converts numbers to float64. Preserve the
		// raw Discord JSON while retaining generic input validation and defaults.
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: json.RawMessage(data),
		}, nil, nil
	})
}

func newServer(c *discordClient, audit *log.Logger) *mcp.Server {
	instructions := "Read-only Discord access. Embed-only messages are exposed through display_text, rendered_embeds, and raw embeds. No mutation tools exist."
	if c.config.enableWrite {
		instructions = "Discord reads and explicitly enabled writes. Mass mentions require opt-in; deletions require current-name confirmation; webhook secrets are saved only to private files."
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "cordglean", Version: "0.2.0"}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	var reference struct {
		Tools []*mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(readToolsJSON, &reference); err != nil {
		panic("invalid embedded read schemas")
	}
	definitions := make(map[string]*mcp.Tool)
	order := make(map[string]int)
	for i, tool := range reference.Tools {
		definitions[tool.Name] = tool
		order[tool.Name] = i
	}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if list, ok := result.(*mcp.ListToolsResult); err == nil && ok && list != nil && method == "tools/list" {
				rank := func(name string) int {
					if value, ok := order[name]; ok {
						return value
					}
					return len(order)
				}
				sort.SliceStable(list.Tools, func(i, j int) bool { return rank(list.Tools[i].Name) < rank(list.Tools[j].Name) })
			}
			return result, err
		}
	})
	addReadTool(s, definitions["read_channel"], func(ctx context.Context, in channelReadInput) (object, error) {
		return c.readChannel(ctx, in.ChannelID, in.Limit, in.Before, in.After)
	})
	addReadTool(s, definitions["get_message"], func(ctx context.Context, in messageReadInput) (object, error) {
		return c.getMessage(ctx, in.ChannelID, in.MessageID, in.IncludeRawEmbeds)
	})
	addReadTool(s, definitions["read_thread"], func(ctx context.Context, in threadReadInput) (object, error) {
		out, err := c.readChannel(ctx, in.ThreadID, in.Limit, in.Before, in.After)
		if err == nil {
			out["thread_id"] = out["channel_id"]
			delete(out, "channel_id")
		}
		return out, err
	})
	addReadTool(s, definitions["list_channels"], func(ctx context.Context, in guildInput) (object, error) {
		return c.listChannels(ctx, in.GuildID)
	})
	addReadTool(s, definitions["read_user_profile"], func(ctx context.Context, in userReadInput) (object, error) {
		return c.readUserProfile(ctx, in.UserID, in.GuildID)
	})
	if c.config.enableWrite {
		if audit == nil {
			audit = log.New(os.Stderr, "", 0)
		}
		registerWriteTools(s, &writeService{client: c, audit: audit})
	}
	return s
}
