package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func humanDuration(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}

func describeWait(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes < 1 {
		return "less than a minute"
	}
	if minutes < 60 {
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}

	hours := minutes / 60
	rest := minutes % 60
	text := fmt.Sprintf("%d hours", hours)
	if hours == 1 {
		text = "1 hour"
	}
	if rest > 0 {
		text += fmt.Sprintf(" %d min", rest)
	}
	return text
}

func describeAge(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes < 1 {
		return "under a minute"
	}
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}

	hours := minutes / 60
	if hours < 48 {
		return fmt.Sprintf("%d h", hours)
	}
	return fmt.Sprintf("%d d", hours/24)
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == math.Trunc(v) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func getString(m map[string]any, key string) string {
	return valueString(m[key])
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

func getBool(m map[string]any, key string) bool {
	value, _ := m[key].(bool)
	return value
}

func getList(m map[string]any, key string) []map[string]any {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			items = append(items, object)
		}
	}
	return items
}

func commaInt(n int) string {
	text := strconv.Itoa(n)
	if n < 1000 {
		return text
	}

	start := len(text) % 3
	if start == 0 {
		start = 3
	}

	var out strings.Builder
	out.WriteString(text[:start])
	for i := start; i < len(text); i += 3 {
		out.WriteByte(',')
		out.WriteString(text[i : i+3])
	}
	return out.String()
}
