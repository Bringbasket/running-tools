package mail

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

var aliasTimestampFields = []string{
	"createTimestamp",
	"createdTimestamp",
	"createdAt",
	"createdDate",
	"creationDate",
	"dateCreated",
}

func normalizeAliasTimestamp(alias map[string]any) {
	if alias == nil {
		return
	}
	if timestamp, ok := aliasTimestamp(alias["createTimestamp"]); ok {
		alias["createTimestamp"] = timestamp
		return
	}
	for _, field := range aliasTimestampFields[1:] {
		if timestamp, ok := aliasTimestamp(alias[field]); ok {
			alias["createTimestamp"] = timestamp
			return
		}
	}
}

func aliasTimestamp(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return 0, false
		}
		return float64(typed.UnixMilli()), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return normalizeAliasEpoch(parsed)
	case float64:
		return normalizeAliasEpoch(typed)
	case float32:
		return normalizeAliasEpoch(float64(typed))
	case int:
		return normalizeAliasEpoch(float64(typed))
	case int64:
		return normalizeAliasEpoch(float64(typed))
	case uint64:
		return normalizeAliasEpoch(float64(typed))
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0, false
		}
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			return normalizeAliasEpoch(parsed)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05"} {
			parsed, err := time.Parse(layout, text)
			if err == nil {
				return float64(parsed.UnixMilli()), true
			}
		}
	}
	return 0, false
}

func normalizeAliasEpoch(value float64) (float64, bool) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	// Apple Web responses use milliseconds; older integrations sometimes use
	// Unix seconds. Normalize both to the frontend's millisecond contract.
	if value < 100000000000 {
		value *= 1000
	}
	return value, true
}

func aliasTimestampFromMap(alias map[string]any) (float64, bool) {
	if alias == nil {
		return 0, false
	}
	for _, field := range aliasTimestampFields {
		if timestamp, ok := aliasTimestamp(alias[field]); ok {
			return timestamp, true
		}
	}
	return 0, false
}
