package main

import (
	"errors"
	"strings"
	"unicode/utf8"
)

func validateEmbeds(embeds []object) error {
	if len(embeds) > 10 {
		return errors.New("a message supports at most 10 embeds")
	}
	total := 0
	field := func(m object, key string, limit int) error {
		value, present := m[key]
		if !present {
			return nil
		}
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) {
			return errors.New("embed text fields must be strings")
		}
		n := utf8.RuneCountInString(strings.TrimSpace(s))
		if n > limit {
			return errors.New("embed text field exceeds Discord limits")
		}
		total += n
		return nil
	}
	for _, embed := range embeds {
		if embed == nil {
			return errors.New("embeds must contain objects")
		}
		for key, limit := range map[string]int{"title": 256, "description": 4096} {
			if err := field(embed, key, limit); err != nil {
				return err
			}
		}
		for key, spec := range map[string]struct {
			text  string
			limit int
		}{"footer": {"text", 2048}, "author": {"name", 256}} {
			if value, exists := embed[key]; exists {
				m := mapping(value)
				if m == nil {
					return errors.New("embed author and footer must be objects")
				}
				if err := field(m, spec.text, spec.limit); err != nil {
					return err
				}
			}
		}
		if value, exists := embed["fields"]; exists {
			fields, ok := value.([]any)
			if !ok || len(fields) > 25 {
				return errors.New("an embed supports at most 25 field objects")
			}
			for _, value := range fields {
				m := mapping(value)
				if m == nil {
					return errors.New("embed fields must contain objects")
				}
				if err := field(m, "name", 256); err != nil {
					return err
				}
				if err := field(m, "value", 1024); err != nil {
					return err
				}
			}
		}
	}
	if total > 6000 {
		return errors.New("embed text exceeds the 6000 character message limit")
	}
	return nil
}
