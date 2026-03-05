package veo

import "testing"

func TestNewClient(t *testing.T) {
	c := NewClient("key123", "veo-3.1-generate-preview", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if c.model != "veo-3.1-generate-preview" {
		t.Errorf("expected model=veo-3.1-generate-preview, got %s", c.model)
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}

func TestExtractVideoURI(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected string
	}{
		{
			name:     "empty map",
			input:    map[string]interface{}{},
			expected: "",
		},
		{
			name: "no response key",
			input: map[string]interface{}{
				"done": true,
			},
			expected: "",
		},
		{
			name: "full valid response",
			input: map[string]interface{}{
				"done": true,
				"response": map[string]interface{}{
					"generateVideoResponse": map[string]interface{}{
						"generatedSamples": []interface{}{
							map[string]interface{}{
								"video": map[string]interface{}{
									"uri": "https://example.com/video.mp4",
								},
							},
						},
					},
				},
			},
			expected: "https://example.com/video.mp4",
		},
		{
			name: "empty samples array",
			input: map[string]interface{}{
				"response": map[string]interface{}{
					"generateVideoResponse": map[string]interface{}{
						"generatedSamples": []interface{}{},
					},
				},
			},
			expected: "",
		},
		{
			name: "missing video key",
			input: map[string]interface{}{
				"response": map[string]interface{}{
					"generateVideoResponse": map[string]interface{}{
						"generatedSamples": []interface{}{
							map[string]interface{}{
								"other": "data",
							},
						},
					},
				},
			},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractVideoURI(tc.input)
			if got != tc.expected {
				t.Errorf("extractVideoURI() = %q, want %q", got, tc.expected)
			}
		})
	}
}
