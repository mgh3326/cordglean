package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// Discord channel types that take threads: text, forum and media.
const (
	channelTypeText  = "0"
	channelTypeForum = "15"
	channelTypeMedia = "16"
)

type createThreadInput struct {
	ChannelID           string  `json:"channel_id"`
	Name                string  `json:"name"`
	Content             *string `json:"content,omitempty"`
	MessageID           *string `json:"message_id,omitempty"`
	AutoArchiveDuration *int    `json:"auto_archive_duration,omitempty"`
}

type listActiveThreadsInput struct {
	ChannelID       string `json:"channel_id"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           int    `json:"limit"`
}

func validArchiveDuration(minutes int) bool {
	switch minutes {
	case 60, 1440, 4320, 10080:
		return true
	}
	return false
}

// createThread opens a forum or media post, a thread started from a text-channel message, or a
// standalone public thread. Mentions in the forum post's first message are suppressed, as in
// send_message's default. A failed POST is reported as unknown and never retried here.
func (w *writeService) createThread(ctx context.Context, in createThreadInput) (object, error) {
	if err := boundedText(in.Name, "name", 1, 100); err != nil {
		return nil, err
	}
	if in.AutoArchiveDuration != nil && !validArchiveDuration(*in.AutoArchiveDuration) {
		return nil, errors.New("auto_archive_duration must be 60, 1440, 4320 or 10080")
	}
	channel, err := w.channel(ctx, in.ChannelID, "Create Public Threads")
	if err != nil {
		return nil, err
	}
	body := object{"name": in.Name}
	if in.AutoArchiveDuration != nil {
		body["auto_archive_duration"] = *in.AutoArchiveDuration
	}
	var path string
	switch text(channel["type"]) {
	case channelTypeForum, channelTypeMedia:
		if in.MessageID != nil {
			return nil, errors.New("message_id applies only to text channels")
		}
		if in.Content == nil {
			return nil, errors.New("content is required for a forum channel")
		}
		if err := boundedText(*in.Content, "content (maximum 2000 characters)", 1, 2000); err != nil {
			return nil, err
		}
		body["message"] = object{"content": *in.Content, "allowed_mentions": object{"parse": []string{}}}
		path = "/channels/" + in.ChannelID + "/threads"
	case channelTypeText:
		if in.Content != nil {
			return nil, errors.New("content applies only to forum channels; start a text-channel thread from message_id")
		}
		if in.MessageID != nil {
			if err := snowflake(*in.MessageID, "message_id"); err != nil {
				return nil, err
			}
			path = "/channels/" + in.ChannelID + "/messages/" + *in.MessageID + "/threads"
		} else {
			body["type"] = 11
			path = "/channels/" + in.ChannelID + "/threads"
		}
	default:
		return nil, errors.New("create_thread requires a text or forum channel")
	}
	payload, err := w.request(ctx, http.MethodPost, path, body, "Create Public Threads")
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "thread")
	if err != nil {
		return nil, err
	}
	id, err := returnedID(m)
	if err != nil {
		return nil, err
	}
	return object{"thread_id": id, "name": m["name"], "parent_id": m["parent_id"]}, nil
}

// listActiveThreads returns the active threads whose parent is channelID. With includeArchived it
// adds one page of the channel's archived public threads and reports whether more pages exist.
func (c *discordClient) listActiveThreads(ctx context.Context, channelID string, includeArchived bool, limit int) (object, error) {
	if err := c.requireChannel(ctx, channelID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	payload, err := c.request(ctx, http.MethodGet, "/channels/"+channelID, nil, "")
	if err != nil {
		return nil, err
	}
	channel, err := requireObject(payload, "channel")
	if err != nil {
		return nil, err
	}
	guildID, ok := channel["guild_id"].(string)
	if !ok || snowflake(guildID, "guild_id") != nil {
		return nil, errors.New("list_active_threads requires a guild channel")
	}
	payload, err = c.request(ctx, http.MethodGet, "/guilds/"+guildID+"/threads/active", nil, "")
	if err != nil {
		return nil, err
	}
	active, err := requireObject(payload, "active threads")
	if err != nil {
		return nil, err
	}
	threads := make([]object, 0)
	for _, item := range threadItems(active["threads"]) {
		if parent, _ := item["parent_id"].(string); parent == channelID {
			threads = append(threads, threadSummary(item))
		}
	}
	out := object{"channel_id": channelID, "include_archived": includeArchived}
	if includeArchived {
		payload, err := c.request(ctx, http.MethodGet, "/channels/"+channelID+"/threads/archived/public?limit="+strconv.Itoa(limit), nil, "")
		if err != nil {
			return nil, err
		}
		archived, err := requireObject(payload, "archived threads")
		if err != nil {
			return nil, err
		}
		for _, item := range threadItems(archived["threads"]) {
			threads = append(threads, threadSummary(item))
		}
		hasMore, _ := archived["has_more"].(bool)
		out["has_more"] = hasMore
	}
	out["thread_count"] = len(threads)
	out["threads"] = threads
	return out, nil
}

func threadItems(value any) []object {
	items, _ := value.([]any)
	result := make([]object, 0, len(items))
	for _, item := range items {
		if m := mapping(item); m != nil {
			result = append(result, m)
		}
	}
	return result
}

func threadSummary(t object) object {
	meta := mapping(t["thread_metadata"])
	return object{
		"id": t["id"], "name": t["name"], "parent_id": t["parent_id"], "owner_id": t["owner_id"],
		"archived": meta["archived"], "message_count": t["message_count"],
		"last_message_id": t["last_message_id"], "created": meta["create_timestamp"],
	}
}
