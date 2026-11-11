package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const archiveWriteBits uint64 = 1<<11 | 1<<35 | 1<<36 | 1<<38

type writeService struct {
	client *discordClient
	audit  *log.Logger
}

func (w *writeService) run(ctx context.Context, tool, target string, fn func(context.Context) (object, error)) (out object, err error) {
	if snowflake(target, "target") != nil {
		target = "invalid"
	}
	outcome := "failure"
	defer func() { w.audit.Printf("tool=%s target=%s outcome=%s", tool, target, outcome) }()
	if !w.client.config.enableWrite {
		outcome = "refused"
		return nil, errors.New("write tools require CORDGLEAN_ENABLE_WRITE=1")
	}
	out, err = fn(ctx)
	if err == nil {
		outcome = "success"
	} else {
		var api *apiError
		var file *secretFileError
		if errors.As(err, &api) && api.unknown {
			outcome = "unknown"
		} else if api == nil && !errors.As(err, &file) {
			outcome = "refused"
		}
	}
	return out, err
}

func (w *writeService) request(ctx context.Context, method, path string, body object, permission string) (any, error) {
	return w.client.request(ctx, method, path, body, permission)
}

func (w *writeService) channel(ctx context.Context, id, permission string) (object, error) {
	if err := w.client.requireChannel(ctx, id); err != nil {
		return nil, err
	}
	payload, err := w.request(ctx, http.MethodGet, "/channels/"+id, nil, permission)
	if err != nil {
		return nil, err
	}
	return requireObject(payload, "channel")
}

func boundedText(value, label string, min, max int) error {
	n := utf8.RuneCountInString(value)
	if !utf8.ValidString(value) || n < min || n > max {
		return errors.New(label + " has an invalid length")
	}
	return nil
}

type sendMessageInput struct {
	ChannelID        string   `json:"channel_id"`
	Content          string   `json:"content"`
	Embeds           []object `json:"embeds,omitempty"`
	AllowMassMention bool     `json:"allow_mass_mention,omitempty"`
	Nonce            string   `json:"nonce,omitempty"`
}

// newNonce returns 24 hex characters, within Discord's 25-character nonce limit.
func newNonce() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("message nonce could not be generated")
	}
	return hex.EncodeToString(b[:]), nil
}

func (w *writeService) sendMessage(ctx context.Context, in sendMessageInput) (object, error) {
	if err := w.client.requireChannel(ctx, in.ChannelID); err != nil {
		return nil, err
	}
	if err := boundedText(in.Content, "content (maximum 2000 characters)", 0, 2000); err != nil {
		return nil, err
	}
	if in.Content == "" && len(in.Embeds) == 0 {
		return nil, errors.New("content or embeds is required")
	}
	if err := validateEmbeds(in.Embeds); err != nil {
		return nil, err
	}
	nonce := in.Nonce
	if nonce == "" {
		var err error
		if nonce, err = newNonce(); err != nil {
			return nil, err
		}
	} else if err := boundedText(nonce, "nonce (maximum 25 characters)", 1, 25); err != nil {
		return nil, err
	}
	embedText, _ := json.Marshal(in.Embeds)
	if !in.AllowMassMention && (strings.Contains(in.Content, "@everyone") || strings.Contains(in.Content, "@here") ||
		strings.Contains(string(embedText), "@everyone") || strings.Contains(string(embedText), "@here")) {
		return nil, errors.New("mass mentions require allow_mass_mention=true")
	}
	parse := []string{}
	if in.AllowMassMention {
		parse = append(parse, "everyone")
	}
	// enforce_nonce makes Discord return the earlier message for a repeated nonce
	// instead of posting a duplicate, within its few-minute window.
	body := object{"content": in.Content, "allowed_mentions": object{"parse": parse}, "nonce": nonce, "enforce_nonce": true}
	permission := "Send Messages"
	if len(in.Embeds) > 0 {
		body["embeds"] = in.Embeds
		permission += " and Embed Links"
	}
	payload, err := w.request(ctx, http.MethodPost, "/channels/"+in.ChannelID+"/messages", body, permission)
	var api *apiError
	if errors.As(err, &api) && api.unknown {
		return nil, &apiError{
			message:    api.message + ". Check the channel's latest messages with read_channel; if the message is missing, retry with nonce=" + nonce + " so Discord does not post it twice",
			statusCode: api.statusCode, unknown: true,
		}
	}
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "message")
	if err != nil {
		return nil, err
	}
	id, err := returnedID(m)
	return object{"message_id": id, "nonce": nonce}, err
}

type createChannelInput struct {
	GuildID  string  `json:"guild_id"`
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id,omitempty"`
	Topic    *string `json:"topic,omitempty"`
}

func (w *writeService) createChannel(ctx context.Context, in createChannelInput) (object, error) {
	if err := snowflake(in.GuildID, "guild_id"); err != nil {
		return nil, err
	}
	if w.client.config.allowedChannels != nil {
		return nil, errors.New("create_channel requires an unrestricted channel policy; new IDs cannot be allowlisted in advance")
	}
	if err := boundedText(in.Name, "name", 1, 100); err != nil {
		return nil, err
	}
	body := object{"name": in.Name, "type": 0}
	if in.ParentID != nil {
		if err := snowflake(*in.ParentID, "parent_id"); err != nil {
			return nil, err
		}
		body["parent_id"] = *in.ParentID
	}
	if in.Topic != nil {
		if err := boundedText(*in.Topic, "topic", 0, 1024); err != nil {
			return nil, err
		}
		body["topic"] = *in.Topic
	}
	payload, err := w.request(ctx, http.MethodPost, "/guilds/"+in.GuildID+"/channels", body, "Manage Channels")
	return channelResult(payload, err)
}

// optionalParent distinguishes omission from null, so edit_channel can undo a category move.
type optionalParent struct {
	set   bool
	value *string
}

func (p *optionalParent) UnmarshalJSON(data []byte) error {
	p.set = true
	return json.Unmarshal(data, &p.value)
}

type editChannelInput struct {
	ChannelID string         `json:"channel_id"`
	Name      *string        `json:"name,omitempty"`
	Topic     *string        `json:"topic,omitempty"`
	ParentID  optionalParent `json:"parent_id,omitempty"`
}

func (w *writeService) editChannel(ctx context.Context, in editChannelInput) (object, error) {
	if err := w.client.requireChannel(ctx, in.ChannelID); err != nil {
		return nil, err
	}
	body := object{}
	if in.Name != nil {
		if err := boundedText(*in.Name, "name", 1, 100); err != nil {
			return nil, err
		}
		body["name"] = *in.Name
	}
	if in.Topic != nil {
		if err := boundedText(*in.Topic, "topic", 0, 1024); err != nil {
			return nil, err
		}
		body["topic"] = *in.Topic
	}
	if in.ParentID.set {
		if in.ParentID.value != nil {
			if err := w.client.requireChannel(ctx, *in.ParentID.value); err != nil {
				return nil, err
			}
		}
		body["parent_id"] = in.ParentID.value
	}
	if len(body) == 0 {
		return nil, errors.New("at least one channel field is required")
	}
	payload, err := w.request(ctx, http.MethodPatch, "/channels/"+in.ChannelID, body, "Manage Channels")
	return channelResult(payload, err)
}

func channelResult(payload any, err error) (object, error) {
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "channel")
	if err != nil {
		return nil, err
	}
	id, err := returnedID(m)
	if err != nil {
		return nil, err
	}
	return object{"channel_id": id, "name": m["name"], "parent_id": m["parent_id"], "topic": m["topic"]}, nil
}

type archiveChannelInput struct {
	ChannelID         string `json:"channel_id"`
	ArchiveCategoryID string `json:"archive_category_id"`
}

func (w *writeService) archiveChannel(ctx context.Context, in archiveChannelInput) (object, error) {
	permission := "Manage Channels and Manage Roles"
	if err := w.client.requireChannel(ctx, in.ArchiveCategoryID); err != nil {
		return nil, err
	}
	channel, err := w.channel(ctx, in.ChannelID, permission)
	if err != nil {
		return nil, err
	}
	guildID, ok := channel["guild_id"].(string)
	if !ok || snowflake(guildID, "guild_id") != nil {
		return nil, errors.New("archive_channel requires a guild channel")
	}
	switch text(channel["type"]) {
	case "0", "2", "5", "13", "15", "16":
	default:
		return nil, errors.New("archive_channel requires a guild channel that can be moved into a category")
	}
	category, err := w.channel(ctx, in.ArchiveCategoryID, permission)
	if err != nil {
		return nil, err
	}
	if text(category["type"]) != "4" || category["guild_id"] != guildID {
		return nil, errors.New("archive_category_id must be a category in the same guild")
	}
	original, ok := channel["permission_overwrites"].([]any)
	if !ok {
		return nil, &apiError{message: "Discord API returned invalid permission overwrites"}
	}
	overwrites := make([]object, 0, len(original)+1)
	found := false
	for _, item := range original {
		m := mapping(item)
		if m == nil {
			return nil, &apiError{message: "Discord API returned invalid permission overwrites"}
		}
		copy := object{}
		for key, value := range m {
			copy[key] = value
		}
		if m["id"] == guildID && text(m["type"]) == "0" {
			allow, e1 := strconv.ParseUint(text(m["allow"]), 10, 64)
			deny, e2 := strconv.ParseUint(text(m["deny"]), 10, 64)
			if e1 != nil || e2 != nil || found {
				return nil, &apiError{message: "Discord API returned invalid everyone overwrite"}
			}
			copy["allow"] = strconv.FormatUint(allow&^archiveWriteBits, 10)
			copy["deny"] = strconv.FormatUint(deny|archiveWriteBits, 10)
			found = true
		}
		overwrites = append(overwrites, copy)
	}
	if !found {
		overwrites = append(overwrites, object{"id": guildID, "type": 0, "allow": "0", "deny": strconv.FormatUint(archiveWriteBits, 10)})
	}
	_, err = w.request(ctx, http.MethodPatch, "/channels/"+in.ChannelID, object{
		"parent_id": in.ArchiveCategoryID, "permission_overwrites": overwrites,
	}, permission)
	if err != nil {
		return nil, err
	}
	return object{
		"channel_id": in.ChannelID, "parent_id": in.ArchiveCategoryID,
		"previous": object{"parent_id": channel["parent_id"], "permission_overwrites": original},
	}, nil
}

type deleteChannelInput struct {
	ChannelID string  `json:"channel_id"`
	Confirm   *string `json:"confirm,omitempty"`
}

func (w *writeService) deleteChannel(ctx context.Context, in deleteChannelInput) (object, error) {
	if in.Confirm == nil || *in.Confirm == "" {
		return nil, errors.New("confirm must equal the channel's current name")
	}
	channel, err := w.channel(ctx, in.ChannelID, "Manage Channels")
	if err != nil {
		return nil, err
	}
	if name, ok := channel["name"].(string); !ok || name != *in.Confirm {
		return nil, errors.New("confirm must equal the channel's current name")
	}
	_, err = w.request(ctx, http.MethodDelete, "/channels/"+in.ChannelID, nil, "Manage Channels")
	return object{"channel_id": in.ChannelID, "deleted": true}, err
}

type listWebhooksInput struct {
	ChannelID *string `json:"channel_id,omitempty"`
	GuildID   *string `json:"guild_id,omitempty"`
}

func (w *writeService) listWebhooks(ctx context.Context, in listWebhooksInput) (object, error) {
	if (in.ChannelID == nil) == (in.GuildID == nil) {
		return nil, errors.New("supply exactly one of channel_id or guild_id")
	}
	var path string
	if in.ChannelID != nil {
		if err := w.client.requireChannel(ctx, *in.ChannelID); err != nil {
			return nil, err
		}
		path = "/channels/" + *in.ChannelID + "/webhooks"
	} else {
		if err := snowflake(*in.GuildID, "guild_id"); err != nil {
			return nil, err
		}
		path = "/guilds/" + *in.GuildID + "/webhooks"
	}
	payload, err := w.request(ctx, http.MethodGet, path, nil, "Manage Webhooks")
	if err != nil {
		return nil, err
	}
	items, ok := payload.([]any)
	if !ok {
		return nil, &apiError{message: "Discord API returned an unexpected webhooks payload"}
	}
	webhooks := make([]object, 0)
	for _, item := range items {
		m, err := requireObject(item, "webhook")
		if err != nil {
			return nil, err
		}
		id, err := returnedID(m)
		if err != nil {
			return nil, err
		}
		channelID, ok := m["channel_id"].(string)
		if !ok || (in.ChannelID == nil && !w.client.config.permitsChannel(channelID)) {
			continue
		}
		var creator any
		if user := mapping(m["user"]); user != nil {
			creator = object{"id": user["id"], "username": user["username"], "global_name": user["global_name"]}
		}
		webhooks = append(webhooks, object{"id": id, "name": m["name"], "channel_id": channelID, "creator": creator})
	}
	return object{"webhooks": webhooks, "webhook_count": len(webhooks)}, nil
}

type createWebhookInput struct {
	ChannelID  string `json:"channel_id"`
	Name       string `json:"name"`
	OutputPath string `json:"output_path"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

func (w *writeService) createWebhook(ctx context.Context, in createWebhookInput) (object, error) {
	if err := w.client.requireChannel(ctx, in.ChannelID); err != nil {
		return nil, err
	}
	if err := boundedText(in.Name, "name", 1, 80); err != nil {
		return nil, err
	}
	lower := strings.ToLower(in.Name)
	if strings.Contains(lower, "clyde") || strings.Contains(lower, "discord") {
		return nil, errors.New("webhook name must not contain clyde or discord")
	}
	dest, err := prepareSecretFile(in.OutputPath, in.Overwrite)
	if err != nil {
		return nil, err
	}
	defer dest.abort()
	payload, err := w.request(ctx, http.MethodPost, "/channels/"+in.ChannelID+"/webhooks", object{"name": in.Name}, "Manage Webhooks")
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "webhook")
	if err != nil {
		return nil, err
	}
	id, err := returnedID(m)
	if err != nil {
		return nil, err
	}
	token, ok := m["token"].(string)
	if !ok || token == "" || strings.ContainsAny(token, "/\r\n?#") {
		err = &apiError{message: "Discord API returned an invalid webhook token"}
	} else {
		err = dest.commit("https://discord.com/api/webhooks/" + id + "/" + token + "\n")
	}
	if err != nil {
		// A local persistence failure must not leave an unknown live webhook if cleanup is possible.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if _, cleanupErr := w.request(cleanupCtx, http.MethodDelete, "/webhooks/"+id, nil, "Manage Webhooks"); cleanupErr != nil {
			return nil, &secretFileError{message: "webhook secret could not be saved; cleanup failed for webhook " + id}
		}
		return nil, err
	}
	return object{"id": id, "name": in.Name, "path": dest.path}, nil
}

type deleteWebhookInput struct {
	WebhookID string  `json:"webhook_id"`
	Confirm   *string `json:"confirm,omitempty"`
}

func (w *writeService) deleteWebhook(ctx context.Context, in deleteWebhookInput) (object, error) {
	if err := snowflake(in.WebhookID, "webhook_id"); err != nil {
		return nil, err
	}
	if in.Confirm == nil || *in.Confirm == "" {
		return nil, errors.New("confirm must equal the webhook's current name")
	}
	payload, err := w.request(ctx, http.MethodGet, "/webhooks/"+in.WebhookID, nil, "Manage Webhooks")
	if err != nil {
		return nil, err
	}
	m, err := requireObject(payload, "webhook")
	if err != nil {
		return nil, err
	}
	channelID, ok := m["channel_id"].(string)
	if !ok {
		return nil, &apiError{message: "Discord API returned an invalid webhook channel"}
	}
	if err := w.client.requireChannel(ctx, channelID); err != nil {
		return nil, err
	}
	if name, ok := m["name"].(string); !ok || name != *in.Confirm {
		return nil, errors.New("confirm must equal the webhook's current name")
	}
	_, err = w.request(ctx, http.MethodDelete, "/webhooks/"+in.WebhookID, nil, "Manage Webhooks")
	return object{"webhook_id": in.WebhookID, "deleted": true}, err
}
