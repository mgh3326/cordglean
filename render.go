package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func valueOr(m object, key string, fallback any) any {
	if value, ok := m[key]; ok {
		return value
	}
	return fallback
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		n, _ := v.Float64()
		return n != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func text(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(v)
	}
}

func mapping(value any) object {
	result, _ := value.(map[string]any)
	return result
}

func objectList(value any) []object {
	result := make([]object, 0)
	items, _ := value.([]any)
	for _, item := range items {
		if m := mapping(item); m != nil {
			result = append(result, m)
		}
	}
	return result
}

func renderEmbed(embed object, index int) object {
	fields := make([]object, 0)
	for _, field := range objectList(embed["fields"]) {
		fields = append(fields, object{
			"name": valueOr(field, "name", ""), "value": valueOr(field, "value", ""),
			"inline": truthy(field["inline"]),
		})
	}
	footer, author, provider := mapping(embed["footer"]), mapping(embed["author"]), mapping(embed["provider"])
	thumbnail, image, video := mapping(embed["thumbnail"]), mapping(embed["image"]), mapping(embed["video"])
	lines := []string{fmt.Sprintf("[Embed %d]", index)}
	if truthy(embed["title"]) {
		lines = append(lines, "Title: "+text(embed["title"]))
	}
	if truthy(embed["description"]) {
		lines = append(lines, text(embed["description"]))
	}
	if truthy(embed["url"]) {
		lines = append(lines, "URL: "+text(embed["url"]))
	}
	if len(author) > 0 {
		line := "Author: " + text(valueOr(author, "name", ""))
		if truthy(author["url"]) {
			line += " (" + text(author["url"]) + ")"
		}
		lines = append(lines, line)
	}
	for i, field := range fields {
		inline := ""
		if field["inline"] == true {
			inline = " inline"
		}
		lines = append(lines, fmt.Sprintf("Field %d%s — %s:", i+1, inline, text(field["name"])), text(field["value"]))
	}
	if len(footer) > 0 {
		lines = append(lines, "Footer: "+text(valueOr(footer, "text", "")))
	}
	if truthy(embed["timestamp"]) {
		lines = append(lines, "Timestamp: "+text(embed["timestamp"]))
	}
	if truthy(provider["name"]) {
		lines = append(lines, "Provider: "+text(provider["name"]))
	}
	for _, item := range []struct {
		label string
		value object
	}{{"Thumbnail", thumbnail}, {"Image", image}, {"Video", video}} {
		if truthy(item.value["url"]) {
			lines = append(lines, item.label+": "+text(item.value["url"]))
		}
	}
	result := object{"text": strings.Join(lines, "\n"), "fields": fields}
	for _, key := range []string{"title", "description", "url", "type", "color", "flags", "timestamp"} {
		result[key] = embed[key]
	}
	// A typed nil map must become JSON null, while an empty object stays {}.
	for key, value := range map[string]object{
		"footer": footer, "author": author, "provider": provider,
		"thumbnail": thumbnail, "image": image, "video": video,
	} {
		if value == nil {
			result[key] = nil
		} else {
			result[key] = value
		}
	}
	return result
}

func renderAttachment(attachment object) object {
	result := object{"raw": attachment}
	for _, key := range []string{
		"id", "filename", "title", "description", "content_type", "size", "url", "proxy_url",
		"height", "width", "placeholder", "placeholder_version", "ephemeral", "duration_secs",
		"waveform", "flags", "clip_participants", "clip_created_at", "application",
	} {
		if value, ok := attachment[key]; ok {
			result[key] = value
		}
	}
	return result
}

func renderMessage(message object, includeRaw bool) object {
	embeds := objectList(message["embeds"])
	rendered := make([]object, 0, len(embeds))
	attachments := make([]object, 0)
	parts := make([]string, 0)
	if truthy(message["content"]) {
		parts = append(parts, text(message["content"]))
	}
	for i, embed := range embeds {
		item := renderEmbed(embed, i+1)
		rendered = append(rendered, item)
		parts = append(parts, item["text"].(string))
	}
	for _, attachment := range objectList(message["attachments"]) {
		item := renderAttachment(attachment)
		attachments = append(attachments, item)
		label := valueOr(item, "filename", valueOr(item, "id", "attachment"))
		detail := "[Attachment] " + text(label)
		if truthy(item["content_type"]) {
			detail += " (" + text(item["content_type"]) + ")"
		}
		if item["size"] != nil {
			detail += ", " + text(item["size"]) + " bytes"
		}
		if truthy(item["url"]) {
			detail += "\n" + text(item["url"])
		}
		parts = append(parts, detail)
	}
	result := object{
		"content": valueOr(message, "content", ""), "display_text": strings.Join(parts, "\n\n"),
		"pinned": valueOr(message, "pinned", false), "tts": valueOr(message, "tts", false),
		"mention_everyone": valueOr(message, "mention_everyone", false),
		"mentions":         valueOr(message, "mentions", []any{}), "mention_roles": valueOr(message, "mention_roles", []any{}),
		"rendered_embeds": rendered, "attachments": attachments,
	}
	for _, key := range []string{
		"id", "channel_id", "guild_id", "type", "timestamp", "edited_timestamp", "author",
		"webhook_id", "message_reference", "flags",
	} {
		result[key] = message[key]
	}
	if includeRaw {
		result["embeds"] = embeds
	}
	return result
}
