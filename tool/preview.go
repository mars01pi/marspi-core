package tool

import (
	"encoding/json"
	"fmt"
)

func toStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case bool:
		return fmt.Sprintf("%v", typed)
	default:
		data, _ := json.Marshal(typed)
		return string(data)
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
