package contracts

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
)

// JSONMap 是 map[string]any 的数据库/JSON 统一封装。
type JSONMap map[string]any

// JSONStringSlice 是 []string 的数据库/JSON 统一封装。
type JSONStringSlice []string

// JSONUintSlice 是 []uint 的数据库/JSON 统一封装。
type JSONUintSlice []uint

func (m JSONMap) Value() (driver.Value, error) {
	return m.String(), nil
}

func (m *JSONMap) Scan(value any) error {
	out, ok := decodeRawJSON(value)
	if !ok {
		*m = JSONMap{}
		return nil
	}
	parsed, err := parseJSONMap(out)
	if err != nil {
		*m = JSONMap{}
		return nil
	}
	*m = parsed
	return nil
}

func (m JSONMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(normalizeJSONMap(m))
}

func (m *JSONMap) UnmarshalJSON(data []byte) error {
	parsed, err := parseJSONMap(data)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func (m JSONMap) String() string {
	b, err := json.Marshal(normalizeJSONMap(m))
	if err != nil {
		return "{}"
	}
	return strings.TrimSpace(string(b))
}

// JSONMapFromAny 将任意输入归一化为 JSONMap。
func JSONMapFromAny(v any) JSONMap {
	switch t := v.(type) {
	case nil:
		return JSONMap{}
	case JSONMap:
		return cloneJSONMap(t)
	case map[string]any:
		return cloneJSONMap(JSONMap(t))
	case string:
		parsed, err := parseJSONMap([]byte(strings.TrimSpace(t)))
		if err != nil {
			return JSONMap{}
		}
		return parsed
	case []byte:
		parsed, err := parseJSONMap(t)
		if err != nil {
			return JSONMap{}
		}
		return parsed
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return JSONMap{}
		}
		parsed, err := parseJSONMap(b)
		if err != nil {
			return JSONMap{}
		}
		return parsed
	}
}

func (s JSONStringSlice) Value() (driver.Value, error) {
	return s.String(), nil
}

func (s *JSONStringSlice) Scan(value any) error {
	out, ok := decodeRawJSON(value)
	if !ok {
		*s = JSONStringSlice{}
		return nil
	}
	parsed, err := parseJSONStringSlice(out)
	if err != nil {
		*s = JSONStringSlice{}
		return nil
	}
	*s = parsed
	return nil
}

func (s JSONStringSlice) MarshalJSON() ([]byte, error) {
	return json.Marshal(normalizeJSONStringSlice(s))
}

func (s *JSONStringSlice) UnmarshalJSON(data []byte) error {
	parsed, err := parseJSONStringSlice(data)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

func (s JSONStringSlice) String() string {
	b, err := json.Marshal(normalizeJSONStringSlice(s))
	if err != nil {
		return "[]"
	}
	return strings.TrimSpace(string(b))
}

// JSONStringSliceFromAny 将任意输入归一化为 JSONStringSlice。
func JSONStringSliceFromAny(v any) JSONStringSlice {
	switch t := v.(type) {
	case nil:
		return JSONStringSlice{}
	case JSONStringSlice:
		return append(JSONStringSlice{}, t...)
	case []string:
		return append(JSONStringSlice{}, t...)
	case string:
		parsed, err := parseJSONStringSlice([]byte(strings.TrimSpace(t)))
		if err != nil {
			return JSONStringSlice{}
		}
		return parsed
	case []byte:
		parsed, err := parseJSONStringSlice(t)
		if err != nil {
			return JSONStringSlice{}
		}
		return parsed
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return JSONStringSlice{}
		}
		parsed, err := parseJSONStringSlice(b)
		if err != nil {
			return JSONStringSlice{}
		}
		return parsed
	}
}

func (s JSONUintSlice) Value() (driver.Value, error) {
	return s.String(), nil
}

func (s *JSONUintSlice) Scan(value any) error {
	out, ok := decodeRawJSON(value)
	if !ok {
		*s = JSONUintSlice{}
		return nil
	}
	parsed, err := parseJSONUintSlice(out)
	if err != nil {
		*s = JSONUintSlice{}
		return nil
	}
	*s = parsed
	return nil
}

func (s JSONUintSlice) MarshalJSON() ([]byte, error) {
	return json.Marshal(normalizeJSONUintSlice(s))
}

func (s *JSONUintSlice) UnmarshalJSON(data []byte) error {
	parsed, err := parseJSONUintSlice(data)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

func (s JSONUintSlice) String() string {
	b, err := json.Marshal(normalizeJSONUintSlice(s))
	if err != nil {
		return "[]"
	}
	return strings.TrimSpace(string(b))
}

// JSONUintSliceFromAny 将任意输入归一化为 JSONUintSlice。
func JSONUintSliceFromAny(v any) JSONUintSlice {
	switch t := v.(type) {
	case nil:
		return JSONUintSlice{}
	case JSONUintSlice:
		return append(JSONUintSlice{}, t...)
	case []uint:
		return append(JSONUintSlice{}, t...)
	case string:
		parsed, err := parseJSONUintSlice([]byte(strings.TrimSpace(t)))
		if err != nil {
			return JSONUintSlice{}
		}
		return parsed
	case []byte:
		parsed, err := parseJSONUintSlice(t)
		if err != nil {
			return JSONUintSlice{}
		}
		return parsed
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return JSONUintSlice{}
		}
		parsed, err := parseJSONUintSlice(b)
		if err != nil {
			return JSONUintSlice{}
		}
		return parsed
	}
}

func cloneJSONMap(in JSONMap) JSONMap {
	if len(in) == 0 {
		return JSONMap{}
	}
	out := make(JSONMap, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeJSONMap(in JSONMap) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	return map[string]any(cloneJSONMap(in))
}

func normalizeJSONStringSlice(in JSONStringSlice) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string{}, in...)
}

func normalizeJSONUintSlice(in JSONUintSlice) []uint {
	if len(in) == 0 {
		return []uint{}
	}
	return append([]uint{}, in...)
}

func parseJSONMap(raw []byte) (JSONMap, error) {
	raw = trimJSONRaw(raw)
	if len(raw) == 0 || strings.EqualFold(string(raw), "null") {
		return JSONMap{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return JSONMap{}, err
	}
	if out == nil {
		return JSONMap{}, nil
	}
	return JSONMap(out), nil
}

func parseJSONStringSlice(raw []byte) (JSONStringSlice, error) {
	raw = trimJSONRaw(raw)
	if len(raw) == 0 || strings.EqualFold(string(raw), "null") {
		return JSONStringSlice{}, nil
	}
	out := []string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return JSONStringSlice{}, err
	}
	if out == nil {
		return JSONStringSlice{}, nil
	}
	return JSONStringSlice(out), nil
}

func parseJSONUintSlice(raw []byte) (JSONUintSlice, error) {
	raw = trimJSONRaw(raw)
	if len(raw) == 0 || strings.EqualFold(string(raw), "null") {
		return JSONUintSlice{}, nil
	}
	out := []uint{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return JSONUintSlice{}, err
	}
	if out == nil {
		return JSONUintSlice{}, nil
	}
	return JSONUintSlice(out), nil
}

func decodeRawJSON(value any) ([]byte, bool) {
	switch t := value.(type) {
	case nil:
		return nil, true
	case []byte:
		return t, true
	case string:
		return []byte(t), true
	case json.RawMessage:
		return []byte(t), true
	case JSONMap:
		return []byte(t.String()), true
	case JSONStringSlice:
		return []byte(t.String()), true
	case JSONUintSlice:
		return []byte(t.String()), true
	case map[string]any, []string, []uint:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, false
		}
		return b, true
	default:
		return nil, false
	}
}

func trimJSONRaw(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
