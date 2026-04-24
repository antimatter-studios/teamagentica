package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-google/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-google/internal/usage"
)

// GeminiAdapter implements agentkit.ProviderAdapter using the Gemini API.
// It translates between agentkit's provider-agnostic types and the Gemini
// API format, handling streaming, function calling, and image attachments.
type GeminiAdapter struct {
	mu      sync.RWMutex
	apiKey  string
	model   string
	debug   bool
	client  *gemini.Client
	tracker *usage.Tracker

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	APIKey  string
	Model   string
	Debug   bool
	Tracker *usage.Tracker
}

// NewAdapter creates a GeminiAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *GeminiAdapter {
	return &GeminiAdapter{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		debug:   cfg.Debug,
		client:  gemini.NewClient(cfg.APIKey, cfg.Debug),
		tracker: cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *GeminiAdapter) ProviderName() string { return "gemini" }

// ModelID implements agentkit.ProviderAdapter.
func (a *GeminiAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetEmitEvent sets the event emission callback.
func (a *GeminiAdapter) SetEmitEvent(fn func(string, string)) {
	a.emitEvent = fn
}

// ApplyConfig updates mutable config fields in-place.
func (a *GeminiAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["GEMINI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	rebuildClient := false
	if v, ok := config["GEMINI_API_KEY"]; ok {
		if v != a.apiKey {
			a.apiKey = v
			rebuildClient = true
		}
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		newDebug := v == "true"
		if newDebug != a.debug {
			a.debug = newDebug
			rebuildClient = true
		}
	}
	if rebuildClient {
		a.client = gemini.NewClient(a.apiKey, a.debug)
		log.Printf("[config] rebuilt gemini client (debug=%v)", a.debug)
	}
}

// StreamChat implements agentkit.ProviderAdapter. It calls the Gemini streaming
// API and forwards text/tool events through the agentkit EventSink.
//
// The agentkit runtime handles the tool loop: when this method returns
// FinishReasonToolUse, the runtime executes the tool calls and calls
// StreamChat again with the results appended to the messages.
func (a *GeminiAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	apiKey := a.apiKey
	debug := a.debug
	a.mu.RUnlock()

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("no API key configured — set GEMINI_API_KEY")
	}

	model := req.Model
	if model == "" {
		model = a.ModelID()
	}

	// Convert agentkit messages to Gemini format.
	messages := toGeminiMessages(req.Messages)

	// System prompt.
	if req.SystemPrompt != "" {
		filtered := make([]gemini.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]gemini.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	// Convert agentkit tool definitions to Gemini function declarations.
	var toolDefs []gemini.FunctionDeclaration
	if len(req.Tools) > 0 {
		toolDefs = toGeminiFunctionDeclarations(req.Tools)
	}

	// Forward image URLs from the current user turn to the Gemini backend.
	imageURLs := req.ImageURLs

	// Call Gemini streaming API.
	var stream <-chan gemini.StreamEvent
	if len(toolDefs) > 0 {
		stream = a.client.StreamChatCompletionWithTools(ctx, model, messages, toolDefs, imageURLs)
	} else {
		stream = a.client.StreamChatCompletion(ctx, model, messages, imageURLs)
	}

	var toolCalls []agentkit.ToolCall
	var totalUsage agentkit.Usage

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Gemini error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("gemini stream: %v", ev.Err)
		}

		if ev.Text != "" {
			sink.SendText(ev.Text)
		}

		if ev.FunctionCall != nil {
			argsJSON, _ := json.Marshal(ev.FunctionCall.Args)
			tc := agentkit.ToolCall{
				ID:        ev.FunctionCall.Name, // Gemini doesn't use separate IDs
				Name:      ev.FunctionCall.Name,
				Arguments: argsJSON,
			}
			toolCalls = append(toolCalls, tc)
			sink.SendToolCall(tc)

			if debug {
				log.Printf("[stream] tool_call: %s", ev.FunctionCall.Name)
			}
		}

		if ev.Usage != nil {
			totalUsage.InputTokens += ev.Usage.PromptTokens
			totalUsage.OutputTokens += ev.Usage.CompletionTokens
			totalUsage.CachedTokens += ev.Usage.CachedTokens
		}
	}

	// Record local usage.
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalUsage.InputTokens,
			OutputTokens: totalUsage.OutputTokens,
			TotalTokens:  totalUsage.InputTokens + totalUsage.OutputTokens,
			CachedTokens: totalUsage.CachedTokens,
		})
	}

	finishReason := agentkit.FinishReasonEndTurn
	if len(toolCalls) > 0 {
		finishReason = agentkit.FinishReasonToolUse
	}

	return agentkit.ProviderResult{
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
		Usage:        totalUsage,
	}, nil
}

// --- Message conversion helpers ---

// toGeminiMessages converts agentkit messages to Gemini format.
// Handles tool call results by converting them to Gemini function responses.
func toGeminiMessages(msgs []agentkit.Message) []gemini.Message {
	var out []gemini.Message
	for _, m := range msgs {
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			// Assistant message with tool calls — emit as function call messages.
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				if len(tc.Arguments) > 0 {
					json.Unmarshal(tc.Arguments, &args)
				}
				out = append(out, gemini.Message{
					FunctionCallName: tc.Name,
					FunctionCallArgs: args,
				})
			}
		case m.Role == "tool" && m.ToolResult != nil:
			// Tool result — emit as function response.
			var resultData map[string]interface{}
			if err := json.Unmarshal([]byte(m.ToolResult.Content), &resultData); err != nil {
				resultData = map[string]interface{}{"result": m.ToolResult.Content}
			}
			out = append(out, gemini.Message{
				FunctionRespName: m.ToolResult.CallID,
				FunctionRespData: resultData,
			})
		default:
			out = append(out, gemini.Message{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}
	return out
}

// toGeminiFunctionDeclarations converts agentkit tool definitions to Gemini format.
// It sanitizes parameters to remove empty enum values that Gemini rejects.
func toGeminiFunctionDeclarations(tools []agentkit.ToolDefinition) []gemini.FunctionDeclaration {
	defs := make([]gemini.FunctionDeclaration, len(tools))
	for i, t := range tools {
		defs[i] = gemini.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  sanitizeParams(t.Parameters),
		}
	}
	return defs
}

// sanitizeParams cleans up JSON Schema parameters for Gemini compatibility.
// Removes empty strings from enum arrays and strips empty enums entirely.
func sanitizeParams(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	sanitizeObject(obj)
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// sanitizeObject recursively cleans enum arrays and nested properties.
func sanitizeObject(obj map[string]interface{}) {
	// Clean enum arrays — remove empty strings.
	if enumRaw, ok := obj["enum"]; ok {
		if enumArr, ok := enumRaw.([]interface{}); ok {
			var cleaned []interface{}
			for _, v := range enumArr {
				if s, ok := v.(string); ok && s == "" {
					continue
				}
				cleaned = append(cleaned, v)
			}
			if len(cleaned) == 0 {
				delete(obj, "enum")
			} else {
				obj["enum"] = cleaned
			}
		}
	}

	// Recurse into properties.
	if props, ok := obj["properties"].(map[string]interface{}); ok {
		for _, v := range props {
			if propObj, ok := v.(map[string]interface{}); ok {
				sanitizeObject(propObj)
			}
		}
	}

	// Recurse into items (for array types).
	if items, ok := obj["items"].(map[string]interface{}); ok {
		sanitizeObject(items)
	}
}
