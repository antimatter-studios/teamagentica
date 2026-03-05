package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSONStringList is a []string that GORM stores as a JSON text column
// but serializes as a proper JSON array in API responses.
type JSONStringList []string

// Value implements driver.Valuer — writes JSON text to the DB.
func (j JSONStringList) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	data, err := json.Marshal([]string(j))
	if err != nil {
		return "[]", nil
	}
	return string(data), nil
}

// Scan implements sql.Scanner — reads JSON text from the DB.
func (j *JSONStringList) Scan(value interface{}) error {
	if value == nil {
		*j = []string{}
		return nil
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("JSONStringList.Scan: unsupported type %T", value)
	}
	if s == "" {
		*j = []string{}
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		*j = []string{}
		return nil
	}
	*j = arr
	return nil
}

// MarshalJSON serializes as a JSON array.
func (j JSONStringList) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(j))
}

// UnmarshalJSON accepts either a JSON array or a JSON string containing an array.
func (j *JSONStringList) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*j = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		var inner []string
		if err2 := json.Unmarshal([]byte(s), &inner); err2 == nil {
			*j = inner
			return nil
		}
	}
	*j = nil
	return nil
}

// JSONRawString is a raw JSON value stored as text in the DB.
// It serializes as the parsed JSON object/array in API responses,
// not as an escaped string.
type JSONRawString json.RawMessage

// Value implements driver.Valuer — writes the raw JSON text to the DB.
func (j JSONRawString) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "", nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner — reads JSON text from the DB.
func (j *JSONRawString) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case string:
		*j = JSONRawString(v)
	case []byte:
		*j = JSONRawString(v)
	default:
		return fmt.Errorf("JSONRawString.Scan: unsupported type %T", value)
	}
	return nil
}

// MarshalJSON returns the raw JSON — no double-encoding.
func (j JSONRawString) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

// UnmarshalJSON stores the raw JSON bytes.
func (j *JSONRawString) UnmarshalJSON(data []byte) error {
	*j = JSONRawString(data)
	return nil
}
