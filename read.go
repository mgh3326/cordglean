package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

func (c *discordClient) readChannel(ctx context.Context, id string, limit int, before, after *string) (object, error) {
	if err := c.requireChannel(ctx, id); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	if before != nil && after != nil {
		return nil, errors.New("before and after are mutually exclusive")
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	for key, value := range map[string]*string{"before": before, "after": after} {
		if value != nil {
			if err := snowflake(*value, key); err != nil {
				return nil, err
			}
			query.Set(key, *value)
		}
	}
	payload, err := c.request(ctx, http.MethodGet, "/channels/"+id+"/messages?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	items, ok := payload.([]any)
	if !ok {
		return nil, &apiError{message: "Discord API returned an unexpected messages payload"}
	}
	messages := make([]object, 0)
	for _, item := range items {
		if m := mapping(item); m != nil {
			messages = append(messages, renderMessage(m, true))
		}
	}
	var newest, oldest any
	if len(messages) > 0 {
		newest, oldest = messages[0]["id"], messages[len(messages)-1]["id"]
	}
	return object{
		"channel_id": id, "order": "newest_to_oldest", "message_count": len(messages),
		"page": object{
			"requested_limit": limit, "before": before, "after": after,
			"newest_message_id": newest, "oldest_message_id": oldest, "possibly_more": len(messages) == limit,
		},
		"messages": messages,
	}, nil
}

func (c *discordClient) getMessage(ctx context.Context, channelID, messageID string, includeRaw bool) (object, error) {
	if err := c.requireChannel(ctx, channelID); err != nil {
		return nil, err
	}
	if err := snowflake(messageID, "message_id"); err != nil {
		return nil, err
	}
	payload, err := c.request(ctx, http.MethodGet, "/channels/"+channelID+"/messages/"+messageID, nil, "")
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "message")
	if err != nil {
		return nil, err
	}
	return renderMessage(m, includeRaw), nil
}

func (c *discordClient) listChannels(ctx context.Context, guildID string) (object, error) {
	if err := snowflake(guildID, "guild_id"); err != nil {
		return nil, err
	}
	payload, err := c.request(ctx, http.MethodGet, "/guilds/"+guildID+"/channels", nil, "")
	if err != nil {
		return nil, err
	}
	items, ok := payload.([]any)
	if !ok {
		return nil, &apiError{message: "Discord API returned an unexpected channels payload"}
	}
	channels := make([]object, 0)
	for _, item := range items {
		if m := mapping(item); m != nil {
			if c.config.permitsListed(m) {
				channels = append(channels, m)
			}
		}
	}
	return object{"guild_id": guildID, "channel_count": len(channels), "channels": channels}, nil
}

func (c *discordClient) readUserProfile(ctx context.Context, userID string, guildID *string) (object, error) {
	if err := snowflake(userID, "user_id"); err != nil {
		return nil, err
	}
	path, source := "/users/"+userID, "user"
	if guildID != nil {
		if err := snowflake(*guildID, "guild_id"); err != nil {
			return nil, err
		}
		path, source = "/guilds/"+*guildID+"/members/"+userID, "guild_member"
	}
	payload, err := c.request(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "user")
	if err != nil {
		return nil, err
	}
	return object{"source": source, "profile": m}, nil
}
