package models

import (
	"encoding/json"
	"testing"
)

// --- JSONStringList tests ---

func TestJSONStringList_Value_Nil(t *testing.T) {
	var j JSONStringList
	v, err := j.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "[]" {
		t.Errorf("Value() = %q, want %q", v, "[]")
	}
}

func TestJSONStringList_Value_NonEmpty(t *testing.T) {
	j := JSONStringList{"a", "b"}
	v, err := j.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != `["a","b"]` {
		t.Errorf("Value() = %q, want %q", v, `["a","b"]`)
	}
}

func TestJSONStringList_Scan_String(t *testing.T) {
	var j JSONStringList
	if err := j.Scan(`["x","y"]`); err != nil {
		t.Fatal(err)
	}
	if len(j) != 2 || j[0] != "x" || j[1] != "y" {
		t.Errorf("Scan = %v", j)
	}
}

func TestJSONStringList_Scan_Bytes(t *testing.T) {
	var j JSONStringList
	if err := j.Scan([]byte(`["a"]`)); err != nil {
		t.Fatal(err)
	}
	if len(j) != 1 || j[0] != "a" {
		t.Errorf("Scan = %v", j)
	}
}

func TestJSONStringList_Scan_Nil(t *testing.T) {
	var j JSONStringList
	if err := j.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if len(j) != 0 {
		t.Errorf("expected empty, got %v", j)
	}
}

func TestJSONStringList_Scan_Empty(t *testing.T) {
	var j JSONStringList
	if err := j.Scan(""); err != nil {
		t.Fatal(err)
	}
	if len(j) != 0 {
		t.Errorf("expected empty, got %v", j)
	}
}

func TestJSONStringList_Scan_InvalidJSON(t *testing.T) {
	var j JSONStringList
	if err := j.Scan("not-json"); err != nil {
		t.Fatal(err)
	}
	if len(j) != 0 {
		t.Errorf("expected empty for invalid JSON, got %v", j)
	}
}

func TestJSONStringList_Scan_UnsupportedType(t *testing.T) {
	var j JSONStringList
	err := j.Scan(12345)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestJSONStringList_MarshalJSON_Nil(t *testing.T) {
	var j JSONStringList
	data, err := j.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Errorf("MarshalJSON() = %s, want []", data)
	}
}

func TestJSONStringList_MarshalJSON_NonEmpty(t *testing.T) {
	j := JSONStringList{"hello"}
	data, err := j.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `["hello"]` {
		t.Errorf("MarshalJSON() = %s", data)
	}
}

func TestJSONStringList_UnmarshalJSON_Array(t *testing.T) {
	var j JSONStringList
	if err := json.Unmarshal([]byte(`["a","b"]`), &j); err != nil {
		t.Fatal(err)
	}
	if len(j) != 2 || j[0] != "a" {
		t.Errorf("got %v", j)
	}
}

func TestJSONStringList_UnmarshalJSON_StringWrapped(t *testing.T) {
	// Handles the case where a JSON string contains a JSON array.
	var j JSONStringList
	if err := json.Unmarshal([]byte(`"[\"x\",\"y\"]"`), &j); err != nil {
		t.Fatal(err)
	}
	if len(j) != 2 || j[0] != "x" || j[1] != "y" {
		t.Errorf("got %v", j)
	}
}

func TestJSONStringList_UnmarshalJSON_Invalid(t *testing.T) {
	var j JSONStringList
	// Should not error — just sets to nil.
	if err := json.Unmarshal([]byte(`12345`), &j); err != nil {
		t.Fatal(err)
	}
	if j != nil {
		t.Errorf("expected nil, got %v", j)
	}
}

// --- JSONRawString tests ---

func TestJSONRawString_Value_Empty(t *testing.T) {
	var j JSONRawString
	v, err := j.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Errorf("Value() = %q, want empty", v)
	}
}

func TestJSONRawString_Value_NonEmpty(t *testing.T) {
	j := JSONRawString(`{"key":"val"}`)
	v, err := j.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != `{"key":"val"}` {
		t.Errorf("Value() = %q", v)
	}
}

func TestJSONRawString_Scan_String(t *testing.T) {
	var j JSONRawString
	if err := j.Scan(`{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if string(j) != `{"a":1}` {
		t.Errorf("Scan = %q", string(j))
	}
}

func TestJSONRawString_Scan_Bytes(t *testing.T) {
	var j JSONRawString
	if err := j.Scan([]byte(`[1,2]`)); err != nil {
		t.Fatal(err)
	}
	if string(j) != `[1,2]` {
		t.Errorf("Scan = %q", string(j))
	}
}

func TestJSONRawString_Scan_Nil(t *testing.T) {
	j := JSONRawString("something")
	if err := j.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if j != nil {
		t.Errorf("expected nil, got %q", string(j))
	}
}

func TestJSONRawString_Scan_UnsupportedType(t *testing.T) {
	var j JSONRawString
	err := j.Scan(42)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestJSONRawString_MarshalJSON_Empty(t *testing.T) {
	var j JSONRawString
	data, err := j.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "null" {
		t.Errorf("MarshalJSON() = %s, want null", data)
	}
}

func TestJSONRawString_MarshalJSON_Object(t *testing.T) {
	j := JSONRawString(`{"x":1}`)
	data, err := j.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"x":1}` {
		t.Errorf("MarshalJSON() = %s", data)
	}
}

func TestJSONRawString_UnmarshalJSON(t *testing.T) {
	var j JSONRawString
	if err := j.UnmarshalJSON([]byte(`{"foo":"bar"}`)); err != nil {
		t.Fatal(err)
	}
	if string(j) != `{"foo":"bar"}` {
		t.Errorf("got %q", string(j))
	}
}

// --- GetCapabilities tests ---

func TestGetCapabilities_Admin(t *testing.T) {
	caps := GetCapabilities("admin")
	if len(caps) == 0 {
		t.Fatal("expected admin capabilities")
	}
	found := false
	for _, c := range caps {
		if c == "system:admin" {
			found = true
		}
	}
	if !found {
		t.Error("admin should have system:admin capability")
	}
}

func TestGetCapabilities_User(t *testing.T) {
	caps := GetCapabilities("user")
	if len(caps) == 0 {
		t.Fatal("expected user capabilities")
	}
	for _, c := range caps {
		if c == "system:admin" {
			t.Error("user should not have system:admin")
		}
	}
}

func TestGetCapabilities_Unknown(t *testing.T) {
	caps := GetCapabilities("nonexistent")
	if len(caps) != 0 {
		t.Errorf("expected empty capabilities for unknown role, got %v", caps)
	}
}
