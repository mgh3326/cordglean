package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addWriteTool[In any](s *mcp.Server, w *writeService, tool *mcp.Tool, target func(In) string, fn func(context.Context, In) (object, error)) {
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := w.run(ctx, tool.Name, target(in), func(ctx context.Context) (object, error) { return fn(ctx, in) })
		return nil, out, err
	})
}

func registerWriteTools(s *mcp.Server, w *writeService) {
	addWriteTool(s, w, &mcp.Tool{
		Name: "send_message", Description: "Post a message. Mass mentions require allow_mass_mention=true; all other pings are suppressed. " +
			"Returns message_id and nonce. After an unknown outcome, read the channel first; retry only with the same nonce so Discord drops a duplicate.",
	}, func(in sendMessageInput) string { return in.ChannelID }, w.sendMessage)
	addWriteTool(s, w, &mcp.Tool{
		Name: "create_channel", Description: "Create a guild text channel, optionally under a category.",
	}, func(in createChannelInput) string { return in.GuildID }, w.createChannel)
	addWriteTool(s, w, &mcp.Tool{
		Name: "edit_channel", Description: "Edit channel fields. An explicit parent_id=null removes the channel from its category.",
		InputSchema: object{
			"type": "object", "required": []string{"channel_id"},
			"properties": object{
				"channel_id": object{"type": "string"}, "name": object{"type": "string"}, "topic": object{"type": "string"},
				"parent_id": object{"type": []string{"string", "null"}},
			},
		},
	}, func(in editChannelInput) string { return in.ChannelID }, w.editChannel)
	addWriteTool(s, w, &mcp.Tool{
		Name: "archive_channel", Description: "Move a guild channel into an archive category and deny @everyone message/thread writes. Returns prior permissions for reversal.",
	}, func(in archiveChannelInput) string { return in.ChannelID }, w.archiveChannel)
	addWriteTool(s, w, &mcp.Tool{
		Name: "delete_channel", Description: "Permanently delete a channel only if confirm exactly equals its current name.",
	}, func(in deleteChannelInput) string { return in.ChannelID }, w.deleteChannel)
	addWriteTool(s, w, &mcp.Tool{
		Name: "list_webhooks", Description: "List webhook names, IDs, channels and creators for exactly one channel or guild; secrets are excluded.",
	}, func(in listWebhooksInput) string {
		if in.ChannelID != nil {
			return *in.ChannelID
		}
		if in.GuildID != nil {
			return *in.GuildID
		}
		return ""
	}, w.listWebhooks)
	addWriteTool(s, w, &mcp.Tool{
		Name: "create_webhook", Description: "Create a webhook and save its URL to a 0600 file. Returns only id/name/path; overwrite defaults false.",
	}, func(in createWebhookInput) string { return in.ChannelID }, w.createWebhook)
	addWriteTool(s, w, &mcp.Tool{
		Name: "delete_webhook", Description: "Permanently delete a webhook only if confirm exactly equals its current name.",
	}, func(in deleteWebhookInput) string { return in.WebhookID }, w.deleteWebhook)
}
