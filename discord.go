package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

const (
	maxRateLimitRetries = 2
	maxRateLimitWait    = 30 * time.Second
)

type object = map[string]any

type apiError struct {
	message    string
	statusCode int
	retryAfter time.Duration
	// unknown marks a write that may have reached Discord: a retry could apply it twice.
	unknown bool
}

func (e *apiError) Error() string { return e.message }

type discordClient struct {
	config config
	http   *http.Client
	gate   chan struct{}
	sleep  func(context.Context, time.Duration) error
}

func newDiscordClient(c config, h *http.Client) *discordClient {
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &discordClient{config: c, http: h, gate: make(chan struct{}, 1), sleep: sleepContext}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// request never returns response bodies on management routes: Discord errors can echo secrets.
func (c *discordClient) request(ctx context.Context, method, path string, body object, permission string) (any, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, &apiError{message: "Discord API request cancelled"}
	}
	// A write that fails after it may have reached Discord is reported as an
	// unknown outcome, so callers read back instead of blindly retrying.
	ambiguous := func(e *apiError) *apiError {
		if method != http.MethodGet {
			e.unknown = true
			e.message = "Discord API write outcome unknown (" + e.message + "); it may have been applied, so read the resource back before retrying"
		}
		return e
	}
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("request contains invalid JSON values")
		}
	}
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.config.apiBaseURL+path, bytes.NewReader(encoded))
		if err != nil {
			return nil, &apiError{message: "Discord API request could not be built"}
		}
		req.Header.Set("Authorization", "Bot "+c.config.token)
		req.Header.Set("User-Agent", "cordglean/0.1 (+https://github.com/mgh3326/cordglean)")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, ambiguous(&apiError{message: "Discord API request failed"})
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			e := &apiError{message: "Discord API response could not be read", statusCode: resp.StatusCode}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, e
			}
			return nil, ambiguous(e)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			delay := retryAfter(resp.Header.Get("Retry-After"), data)
			if attempt == maxRateLimitRetries || delay > maxRateLimitWait {
				return nil, &apiError{message: "Discord API rate limit exceeded", statusCode: 429, retryAfter: delay}
			}
			if err := c.sleep(ctx, delay); err != nil {
				return nil, &apiError{message: "Discord API request cancelled"}
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := fmt.Sprintf("Discord API returned HTTP %d", resp.StatusCode)
			if permission != "" && resp.StatusCode == 403 {
				message += ": bot requires " + permission + "; check channel access and permission overwrites"
			} else if permission == "" {
				var detail struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(data, &detail) == nil && detail.Message != "" {
					message += ": " + detail.Message
				}
			}
			e := &apiError{message: message, statusCode: resp.StatusCode}
			if resp.StatusCode >= 500 {
				return nil, ambiguous(e)
			}
			return nil, e
		}
		if resp.StatusCode == http.StatusNoContent {
			return nil, nil
		}
		var payload any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&payload); err != nil {
			return nil, ambiguous(&apiError{message: "Discord API returned invalid JSON", statusCode: resp.StatusCode})
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, ambiguous(&apiError{message: "Discord API returned invalid JSON", statusCode: resp.StatusCode})
		}
		return payload, nil
	}
	panic("bounded request loop exhausted")
}

func retryAfter(header string, body []byte) time.Duration {
	parse := func(raw string) (time.Duration, bool) {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return 0, false
		}
		if seconds < 0 {
			return 0, false
		}
		// Larger values need no exact conversion: they already exceed the retry bound.
		if seconds > maxRateLimitWait.Seconds() {
			return maxRateLimitWait + time.Second, true
		}
		return time.Duration(seconds * float64(time.Second)), true
	}
	if delay, ok := parse(header); ok {
		return delay
	}
	var payload struct {
		RetryAfter json.Number `json:"retry_after"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if delay, ok := parse(payload.RetryAfter.String()); ok {
			return delay
		}
	}
	return time.Second
}

func requireObject(payload any, kind string) (object, error) {
	value, ok := payload.(map[string]any)
	if !ok {
		return nil, &apiError{message: "Discord API returned an unexpected " + kind + " payload"}
	}
	return value, nil
}

func returnedID(payload object) (string, error) {
	id, ok := payload["id"].(string)
	if !ok || snowflake(id, "id") != nil {
		return "", &apiError{message: "Discord API returned an invalid resource id"}
	}
	return id, nil
}
