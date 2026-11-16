// Package i18n implements REG-06: localised member-facing messages.
// Bundles are JSON files named <locale>.json (e.g. en.json, pcm-NG.json);
// translation lookups fall back to the message key and never panic.
package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Locale is a BCP-47-ish language tag supported by the platform.
type Locale string

const (
	English Locale = "en"
	Yoruba  Locale = "yo"
	Hausa   Locale = "ha"
	Igbo    Locale = "ig"
	Pidgin  Locale = "pcm-NG" // Nigerian Pidgin
)

// Bundle holds the messages for one locale.
type Bundle struct {
	Locale   Locale            `json:"-"`
	Messages map[string]string `json:"messages"`
}

// T translates key, formatting with args when supplied (fmt.Sprintf
// semantics). Missing keys fall back to the key itself so callers always
// get something renderable; T never panics.
func (b Bundle) T(key string, args ...any) string {
	msg, ok := b.Messages[key]
	if !ok || msg == "" {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// LoadDir loads every <locale>.json file in dir into bundles keyed by
// locale. Files must be JSON objects of string→string (or a
// {"messages": {...}} wrapper).
func LoadDir(dir string) (map[Locale]Bundle, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read dir: %w", err)
	}
	bundles := map[Locale]Bundle{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		loc := Locale(strings.TrimSuffix(name, ".json"))
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", name, err)
		}
		msgs, err := parseMessages(raw)
		if err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", name, err)
		}
		bundles[loc] = Bundle{Locale: loc, Messages: msgs}
	}
	return bundles, nil
}

// parseMessages accepts either a flat {"key": "text"} object or the
// wrapped {"messages": {"key": "text"}} form.
func parseMessages(raw []byte) (map[string]string, error) {
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}
	var wrapped struct {
		Messages map[string]string `json:"messages"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Messages == nil {
		return nil, fmt.Errorf("no messages found")
	}
	return wrapped.Messages, nil
}

// Keys returns the sorted message keys (handy for coverage checks).
func (b Bundle) Keys() []string {
	keys := make([]string, 0, len(b.Messages))
	for k := range b.Messages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
