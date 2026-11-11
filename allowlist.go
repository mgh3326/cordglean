package main

import (
	"context"
	"errors"
	"net/http"
)

const errChannelNotAllowed = "channel is not in DISCORD_ALLOWED_CHANNEL_IDS"

// isThread reports whether a channel type is announcement, public or private thread.
func isThread(channelType any) bool {
	switch text(channelType) {
	case "10", "11", "12":
		return true
	}
	return false
}

// permitsListed admits an entry of a channel listing: its own ID is allowed, or it is a
// thread whose parent_id is allowed. The listing already carries parent_id, so no request is made.
func (c config) permitsListed(m object) bool {
	id, ok := m["id"].(string)
	if !ok {
		return false
	}
	if c.permitsChannel(id) {
		return true
	}
	parent, ok := m["parent_id"].(string)
	return ok && isThread(m["type"]) && c.permitsChannel(parent)
}

// requireChannel admits a channel the allowlist names, or a thread whose parent it names.
// A thread's parent is not in the request, so it is read from Discord once per channel.
// Any failure refuses the channel with the same error as an unlisted one.
func (c *discordClient) requireChannel(ctx context.Context, id string) error {
	if err := snowflake(id, "channel_id"); err != nil {
		return err
	}
	if c.config.permitsChannel(id) {
		return nil
	}
	parent, err := c.threadParent(ctx, id)
	if err == nil && parent != "" && c.config.permitsChannel(parent) {
		return nil
	}
	return errors.New(errChannelNotAllowed)
}

// threadParent returns the parent of a thread, or "" for any other channel. Only answers from
// Discord are cached, because a channel's type and parent do not change; a failed read is
// returned, not cached, so the next call asks again.
func (c *discordClient) threadParent(ctx context.Context, id string) (string, error) {
	c.parentsMu.Lock()
	parent, known := c.parents[id]
	c.parentsMu.Unlock()
	if known {
		return parent, nil
	}
	payload, err := c.request(ctx, http.MethodGet, "/channels/"+id, nil, "")
	if err != nil {
		return "", err
	}
	m, err := requireObject(payload, "channel")
	if err != nil {
		return "", err
	}
	parent = ""
	if isThread(m["type"]) {
		parent, _ = m["parent_id"].(string)
	}
	c.parentsMu.Lock()
	c.parents[id] = parent
	c.parentsMu.Unlock()
	return parent, nil
}
