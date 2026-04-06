package kimi

import "testing"

func TestAccumulateToolCalls_NewCall(t *testing.T) {
	existing := []ToolCall{}
	deltas := []ToolCall{
		{ID: "call_1", Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "get_weather", Arguments: `{"city":`}},
	}

	result := AccumulateToolCalls(existing, deltas)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].ID != "call_1" {
		t.Errorf("ID = %q, want %q", result[0].ID, "call_1")
	}
	if result[0].Function.Name != "get_weather" {
		t.Errorf("Name = %q, want %q", result[0].Function.Name, "get_weather")
	}
}

func TestAccumulateToolCalls_AppendArguments(t *testing.T) {
	existing := []ToolCall{
		{ID: "call_1", Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "get_weather", Arguments: `{"city":`}},
	}
	deltas := []ToolCall{
		{Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Arguments: `"London"}`}},
	}

	result := AccumulateToolCalls(existing, deltas)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	want := `{"city":"London"}`
	if result[0].Function.Arguments != want {
		t.Errorf("Arguments = %q, want %q", result[0].Function.Arguments, want)
	}
}

func TestAccumulateToolCalls_MultipleNewCalls(t *testing.T) {
	existing := []ToolCall{}
	deltas := []ToolCall{
		{ID: "call_1", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "a", Arguments: "1"}},
		{ID: "call_2", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "b", Arguments: "2"}},
	}

	result := AccumulateToolCalls(existing, deltas)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
	if result[0].Function.Name != "a" || result[1].Function.Name != "b" {
		t.Errorf("got names %q and %q", result[0].Function.Name, result[1].Function.Name)
	}
}

func TestAccumulateToolCalls_DeltaOnEmpty(t *testing.T) {
	// Delta without ID on empty slice — should not panic.
	deltas := []ToolCall{
		{Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Arguments: "ignored"}},
	}
	result := AccumulateToolCalls(nil, deltas)
	if len(result) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(result))
	}
}

func TestAccumulateToolCalls_NilInputs(t *testing.T) {
	result := AccumulateToolCalls(nil, nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}
