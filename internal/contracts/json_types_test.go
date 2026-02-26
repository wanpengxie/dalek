package contracts

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestJSONMap_ValueAndString(t *testing.T) {
	var empty JSONMap
	val, err := empty.Value()
	if err != nil {
		t.Fatalf("value empty: %v", err)
	}
	if got := val.(string); got != "{}" {
		t.Fatalf("empty value mismatch: got=%q", got)
	}
	if empty.String() != "{}" {
		t.Fatalf("empty string mismatch: %q", empty.String())
	}

	payload := JSONMap{
		"alpha": 1,
		"beta":  "x",
	}
	val, err = payload.Value()
	if err != nil {
		t.Fatalf("value payload: %v", err)
	}
	raw := []byte(val.(string))
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if out["beta"] != "x" {
		t.Fatalf("unexpected payload map: %#v", out)
	}
}

func TestJSONMap_ScanGraceful(t *testing.T) {
	cases := []any{
		nil,
		"",
		" ",
		"null",
		"{}",
		[]byte(`{"k":"v"}`),
		[]byte(`{`), // malformed -> graceful empty
	}
	for _, c := range cases {
		var m JSONMap
		if err := m.Scan(c); err != nil {
			t.Fatalf("scan should not fail for %#v: %v", c, err)
		}
	}

	var m JSONMap
	_ = m.Scan([]byte(`{"k":"v"}`))
	if got := m["k"]; got != "v" {
		t.Fatalf("scan value mismatch: %#v", m)
	}

	_ = m.Scan([]byte(`{`))
	if len(m) != 0 {
		t.Fatalf("malformed should fallback to empty, got=%#v", m)
	}
}

func TestJSONMap_UnmarshalJSON(t *testing.T) {
	var m JSONMap
	if err := json.Unmarshal([]byte(`{"a":1}`), &m); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 key, got=%d", len(m))
	}
	if err := json.Unmarshal([]byte(`null`), &m); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("null should become empty map: %#v", m)
	}
	if err := json.Unmarshal([]byte(`[`), &m); err == nil {
		t.Fatalf("invalid json should return error")
	}
}

func TestJSONMapFromAny(t *testing.T) {
	got := JSONMapFromAny(map[string]any{"x": "y"})
	if got["x"] != "y" {
		t.Fatalf("map input mismatch: %#v", got)
	}
	got = JSONMapFromAny(`{"a":1}`)
	if len(got) != 1 {
		t.Fatalf("string input mismatch: %#v", got)
	}
	got = JSONMapFromAny(`invalid`)
	if len(got) != 0 {
		t.Fatalf("invalid input should return empty map: %#v", got)
	}
}

func TestJSONStringSlice_Roundtrip(t *testing.T) {
	src := JSONStringSlice{"a", "b"}
	val, err := src.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var out JSONStringSlice
	if err := out.Scan(val); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !reflect.DeepEqual([]string(out), []string(src)) {
		t.Fatalf("roundtrip mismatch: got=%v want=%v", out, src)
	}
}

func TestJSONStringSlice_ScanGraceful(t *testing.T) {
	var out JSONStringSlice
	if err := out.Scan([]byte(`[`)); err != nil {
		t.Fatalf("scan malformed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("malformed should fallback empty: %#v", out)
	}
	if err := out.Scan("null"); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("null should become empty slice: %#v", out)
	}
}

func TestJSONStringSliceFromAny(t *testing.T) {
	out := JSONStringSliceFromAny([]string{"x", "y"})
	if !reflect.DeepEqual([]string(out), []string{"x", "y"}) {
		t.Fatalf("from any []string mismatch: %#v", out)
	}
	out = JSONStringSliceFromAny(`["a"]`)
	if !reflect.DeepEqual([]string(out), []string{"a"}) {
		t.Fatalf("from any json mismatch: %#v", out)
	}
	out = JSONStringSliceFromAny(`invalid`)
	if len(out) != 0 {
		t.Fatalf("invalid should fallback empty: %#v", out)
	}
}

func TestJSONUintSlice_Roundtrip(t *testing.T) {
	src := JSONUintSlice{1, 2, 3}
	val, err := src.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var out JSONUintSlice
	if err := out.Scan(val); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !reflect.DeepEqual([]uint(out), []uint(src)) {
		t.Fatalf("roundtrip mismatch: got=%v want=%v", out, src)
	}
}

func TestJSONUintSlice_ScanGraceful(t *testing.T) {
	var out JSONUintSlice
	if err := out.Scan([]byte(`[`)); err != nil {
		t.Fatalf("scan malformed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("malformed should fallback empty: %#v", out)
	}
	if err := out.Scan("null"); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("null should become empty slice: %#v", out)
	}
}

func TestJSONUintSliceFromAny(t *testing.T) {
	out := JSONUintSliceFromAny([]uint{7, 9})
	if !reflect.DeepEqual([]uint(out), []uint{7, 9}) {
		t.Fatalf("from any []uint mismatch: %#v", out)
	}
	out = JSONUintSliceFromAny(`[5]`)
	if !reflect.DeepEqual([]uint(out), []uint{5}) {
		t.Fatalf("from any json mismatch: %#v", out)
	}
	out = JSONUintSliceFromAny(`invalid`)
	if len(out) != 0 {
		t.Fatalf("invalid should fallback empty: %#v", out)
	}
}
