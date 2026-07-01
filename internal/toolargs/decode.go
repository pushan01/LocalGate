package toolargs

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func Decode(raw any, base64Enabled bool) (map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		if base64Enabled {
			if s, ok := extractBase64Envelope(v); ok {
				return decodeBase64String(s)
			}
		}
		return cloneMap(v), nil
	case json.RawMessage:
		if len(v) == 0 || string(v) == "null" {
			return map[string]any{}, nil
		}
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return nil, fmt.Errorf("invalid tool arguments json: %w", err)
		}
		return Decode(decoded, base64Enabled)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return map[string]any{}, nil
		}
		if !base64Enabled {
			return nil, fmt.Errorf("tool arguments must be a JSON object")
		}
		return decodeBase64String(s)
	default:
		return nil, fmt.Errorf("tool arguments must be a JSON object")
	}
}

func extractBase64Envelope(v map[string]any) (string, bool) {
	if len(v) != 1 {
		return "", false
	}
	raw, ok := v["base64data"]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func DecodeHTTPBody(body []byte, base64Enabled bool) (map[string]any, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]any{}, nil
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err == nil {
		return Decode(raw, base64Enabled)
	}
	if !base64Enabled {
		return nil, fmt.Errorf("invalid json body")
	}
	return decodeBase64String(strings.TrimSpace(string(body)))
}

func decodeBase64String(s string) (map[string]any, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 tool arguments: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("invalid base64 tool arguments json: %w", err)
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decoded tool arguments must be a JSON object")
	}
	return obj, nil
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
