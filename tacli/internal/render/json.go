package render

import (
	"encoding/json"
	"fmt"
)

// JSONRenderer outputs structured JSON.
type JSONRenderer struct {
	output map[string]interface{}
	group  string
	items  []map[string]string
}

// NewJSON creates a JSON renderer.
func NewJSON() *JSONRenderer {
	return &JSONRenderer{
		output: map[string]interface{}{},
	}
}

func (j *JSONRenderer) Header(title, subtitle, version string) {
	// Ignored in JSON mode — no decorative output.
}

func (j *JSONRenderer) GroupStart(name string) {
	j.flushGroup()
	j.group = name
	j.items = nil
}

func (j *JSONRenderer) Item(fields map[string]string) {
	// Copy the map to avoid mutation.
	item := make(map[string]string, len(fields))
	for k, v := range fields {
		if v != "" {
			item[k] = v
		}
	}
	j.items = append(j.items, item)
}

func (j *JSONRenderer) Flush() error {
	j.flushGroup()

	data, err := json.MarshalIndent(j.output, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func (j *JSONRenderer) flushGroup() {
	if j.group == "" {
		return
	}
	if len(j.items) == 1 {
		j.output[j.group] = j.items[0]
	} else if len(j.items) > 1 {
		j.output[j.group] = j.items
	}
}
