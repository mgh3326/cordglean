package main

import (
	"errors"
	"net/url"
	"strings"
)

const defaultAPIBaseURL = "https://discord.com/api/v10"

type config struct {
	token           string
	apiBaseURL      string
	allowedChannels map[string]bool
	enableWrite     bool
}

func loadConfig(get func(string) string) (config, error) {
	c := config{token: get("DISCORD_BOT_TOKEN"), apiBaseURL: defaultAPIBaseURL}
	if c.token == "" {
		return c, errors.New("DISCORD_BOT_TOKEN is required in the cordglean process environment")
	}
	if raw := get("DISCORD_API_BASE_URL"); raw != "" {
		c.apiBaseURL = strings.TrimRight(raw, "/")
	}
	u, err := url.Parse(c.apiBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("DISCORD_API_BASE_URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	switch strings.TrimSpace(get("CORDGLEAN_ENABLE_WRITE")) {
	case "", "0":
	case "1":
		c.enableWrite = true
	default:
		return c, errors.New("CORDGLEAN_ENABLE_WRITE must be 0 or 1")
	}
	if raw := strings.TrimSpace(get("DISCORD_ALLOWED_CHANNEL_IDS")); raw != "" {
		c.allowedChannels = make(map[string]bool)
		for id := range strings.SplitSeq(raw, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if err := snowflake(id, "DISCORD_ALLOWED_CHANNEL_IDS entry"); err != nil {
				return c, err
			}
			c.allowedChannels[id] = true
		}
	}
	return c, nil
}

func snowflake(value, label string) error {
	if value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return nil
	}
	return errors.New(label + " must be a Discord snowflake containing only digits")
}

func (c config) permitsChannel(id string) bool {
	return c.allowedChannels == nil || c.allowedChannels[id]
}
