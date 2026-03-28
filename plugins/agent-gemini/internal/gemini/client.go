package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://generativelanguage.googleapis.com/v1beta"

// Message is the standard role+content pair used by the handler layer.
// FunctionCall/FunctionResp fields are used internally for tool-call
// round-trips and are excluded from JSON serialisation.
type Message struct {
	Role               string                 `json:"role"`
	Content            string                 `json:"content"`
	FunctionCallName   string                 `json:"-"`
	FunctionCallArgs   map[string]interface{} `json:"-"`
	ThoughtSignature   string                 `json:"-"` // Gemini thought_signature for function call round-trips.
	FunctionRespName   string                 `json:"-"`
	FunctionRespData   map[string]interface{} `json:"-"`
}

// FunctionDeclaration describes a tool for function calling.
type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// toolConfig wraps function declarations for the API.
type toolConfig struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations"`
}

// FunctionCall is returned by the model when it wants to call a function.
type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}


// functionResponse sends tool results back to the model.
type functionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// Usage holds token counts from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// ChatResponse is the parsed result of a generateContent call.
type ChatResponse struct {
	Content          string
	FunctionCall     *FunctionCall
	ThoughtSignature string // Gemini thought signature to include in function call round-trips.
	Usage            Usage
	Headers          http.Header
}

// part is a piece of content in the Gemini API.
// thoughtSignature is a part-level field required by Gemini for function call round-trips.
type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

// inlineData holds base64-encoded image data for the Gemini API.
type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// content is a message in the Gemini API.
type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

// generateRequest is the request body for generateContent.
type generateRequest struct {
	Contents          []content    `json:"contents"`
	SystemInstruction *content     `json:"systemInstruction,omitempty"`
	Tools             []toolConfig `json:"tools,omitempty"`
}

// Client talks to the Google Gemini API.
type Client struct {
	apiKey     string
	httpClient *http.Client
	debug      bool
}

func NewClient(apiKey string, debug bool) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		debug: debug,
	}
}

// buildContents converts handler-layer Messages into Gemini API content objects.
// System messages are extracted into a separate systemInstruction.
func buildContents(messages []Message) ([]content, *content) {
	var contents []content
	var systemInstruction *content

	for _, m := range messages {
		switch {
		case m.Role == "system":
			systemInstruction = &content{
				Parts: []part{{Text: m.Content}},
			}
		case m.FunctionCallName != "":
			contents = append(contents, content{
				Role: "model",
				Parts: []part{{
					FunctionCall: &FunctionCall{
						Name: m.FunctionCallName,
						Args: m.FunctionCallArgs,
					},
					ThoughtSignature: m.ThoughtSignature,
				}},
			})
		case m.FunctionRespName != "":
			contents = append(contents, content{
				Role: "user",
				Parts: []part{{
					FunctionResponse: &functionResponse{
						Name:     m.FunctionRespName,
						Response: m.FunctionRespData,
					},
				}},
			})
		case m.Role == "assistant":
			contents = append(contents, content{
				Role:  "model",
				Parts: []part{{Text: m.Content}},
			})
		default: // "user" and anything else
			contents = append(contents, content{
				Role:  "user",
				Parts: []part{{Text: m.Content}},
			})
		}
	}
	return contents, systemInstruction
}

// generateContentResponse is the common response shape from generateContent.
type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string              `json:"text"`
				FunctionCall     *FunctionCall `json:"functionCall,omitempty"`
				ThoughtSignature string        `json:"thoughtSignature,omitempty"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		TotalTokenCount         int `json:"totalTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

// parseGenerateResponse extracts text and function call from a parsed response.
func parseGenerateResponse(result *generateContentResponse, headers http.Header) *ChatResponse {
	responseText := ""
	var funcCall *FunctionCall
	var thoughtSig string

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		for _, p := range result.Candidates[0].Content.Parts {
			if p.Text != "" {
				responseText = p.Text
			}
			if p.FunctionCall != nil {
				funcCall = &FunctionCall{
					Name: p.FunctionCall.Name,
					Args: p.FunctionCall.Args,
				}
				thoughtSig = p.ThoughtSignature
			}
		}
	}

	return &ChatResponse{
		Content:          responseText,
		FunctionCall:     funcCall,
		ThoughtSignature: thoughtSig,
		Usage: Usage{
			PromptTokens:     result.UsageMetadata.PromptTokenCount,
			CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      result.UsageMetadata.TotalTokenCount,
			CachedTokens:     result.UsageMetadata.CachedContentTokenCount,
		},
		Headers: headers,
	}
}

// doGenerateContent performs the HTTP call to the generateContent endpoint.
func (c *Client) doGenerateContent(model string, reqBody generateRequest) (*ChatResponse, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", baseURL, model)

	if c.debug {
		log.Printf("[gemini] POST %s body=%s", url, string(jsonBody))
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[gemini] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result generateContentResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return parseGenerateResponse(&result, resp.Header), nil
}

// ChatCompletion sends a chat request to the Gemini generateContent endpoint.
// imageURLs are attached as inline image parts to the last user message.
func (c *Client) ChatCompletion(model string, messages []Message, imageURLs ...string) (*ChatResponse, error) {
	contents, systemInstruction := buildContents(messages)

	// Attach images to the last user message.
	if len(imageURLs) > 0 && len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" {
			for _, imgURL := range imageURLs {
				imgPart, err := c.downloadImagePart(imgURL)
				if err != nil {
					log.Printf("[gemini] failed to download image %s: %v", imgURL, err)
					continue
				}
				last.Parts = append(last.Parts, *imgPart)
			}
		}
	}

	return c.doGenerateContent(model, generateRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	})
}

// ChatCompletionWithTools sends a chat request with tool (function) declarations.
func (c *Client) ChatCompletionWithTools(model string, messages []Message, tools []FunctionDeclaration, imageURLs ...string) (*ChatResponse, error) {
	contents, systemInstruction := buildContents(messages)

	// Attach images to the last user content.
	if len(imageURLs) > 0 && len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" {
			for _, imgURL := range imageURLs {
				imgPart, err := c.downloadImagePart(imgURL)
				if err != nil {
					log.Printf("[gemini] failed to download image %s: %v", imgURL, err)
					continue
				}
				last.Parts = append(last.Parts, *imgPart)
			}
		}
	}

	reqBody := generateRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}
	if len(tools) > 0 {
		reqBody.Tools = []toolConfig{{FunctionDeclarations: tools}}
	}

	return c.doGenerateContent(model, reqBody)
}

// downloadImagePart fetches an image URL and returns it as an inlineData part.
func (c *Client) downloadImagePart(imageURL string) (*part, error) {
	resp, err := c.httpClient.Get(imageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}

	// Telegram returns application/octet-stream; detect from content instead.
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	if c.debug {
		log.Printf("[gemini] downloaded image: %s (%s, %d bytes)", imageURL, mimeType, len(data))
	}

	return &part{
		InlineData: &inlineData{
			MimeType: mimeType,
			Data:     base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

// StreamEvent represents a single chunk from a streaming generateContent response.
type StreamEvent struct {
	Text             string        // Incremental text content.
	FunctionCall     *FunctionCall // Function call if the model wants to invoke a tool.
	ThoughtSignature string        // Gemini thought signature for function call round-trips.
	Usage            *Usage        // Token usage (usually in the final chunk).
	Err              error         // Non-nil if the stream encountered an error.
}

// StreamGenerateContent performs a streaming generateContent call.
// It returns a channel of StreamEvents. The caller should range over the channel.
func (c *Client) StreamGenerateContent(ctx context.Context, model string, reqBody generateRequest) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("marshal request: %w", err)}
			return
		}

		url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", baseURL, model)

		if c.debug {
			log.Printf("[gemini] POST %s (streaming)", url)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("create request: %w", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("http request: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			ch <- StreamEvent{Err: fmt.Errorf("gemini API returned %d: %s", resp.StatusCode, string(body))}
			return
		}

		// Parse SSE stream from Gemini.
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := line[6:]

			var chunk generateContentResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				if c.debug {
					log.Printf("[gemini] failed to parse stream chunk: %v", err)
				}
				continue
			}

			ev := StreamEvent{}

			// Extract text and function calls from candidates.
			if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
				for _, p := range chunk.Candidates[0].Content.Parts {
					if p.Text != "" {
						ev.Text += p.Text
					}
					if p.FunctionCall != nil {
						ev.FunctionCall = &FunctionCall{
							Name: p.FunctionCall.Name,
							Args: p.FunctionCall.Args,
						}
						ev.ThoughtSignature = p.ThoughtSignature
					}
				}
			}

			// Extract usage metadata (typically in the final chunk).
			if chunk.UsageMetadata.TotalTokenCount > 0 {
				ev.Usage = &Usage{
					PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
					CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
					CachedTokens:     chunk.UsageMetadata.CachedContentTokenCount,
				}
			}

			if ev.Text != "" || ev.FunctionCall != nil || ev.Usage != nil {
				ch <- ev
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("stream read: %w", err)}
		}
	}()

	return ch
}

// StreamChatCompletion is a convenience wrapper that builds contents and streams.
func (c *Client) StreamChatCompletion(ctx context.Context, model string, messages []Message, imageURLs []string) <-chan StreamEvent {
	contents, systemInstruction := buildContents(messages)

	if len(imageURLs) > 0 && len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" {
			for _, imgURL := range imageURLs {
				imgPart, err := c.downloadImagePart(imgURL)
				if err != nil {
					log.Printf("[gemini] failed to download image %s: %v", imgURL, err)
					continue
				}
				last.Parts = append(last.Parts, *imgPart)
			}
		}
	}

	return c.StreamGenerateContent(ctx, model, generateRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	})
}

// StreamChatCompletionWithTools is a convenience wrapper that builds contents with tools and streams.
func (c *Client) StreamChatCompletionWithTools(ctx context.Context, model string, messages []Message, tools []FunctionDeclaration, imageURLs []string) <-chan StreamEvent {
	contents, systemInstruction := buildContents(messages)

	if len(imageURLs) > 0 && len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" {
			for _, imgURL := range imageURLs {
				imgPart, err := c.downloadImagePart(imgURL)
				if err != nil {
					log.Printf("[gemini] failed to download image %s: %v", imgURL, err)
					continue
				}
				last.Parts = append(last.Parts, *imgPart)
			}
		}
	}

	reqBody := generateRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}
	if len(tools) > 0 {
		reqBody.Tools = []toolConfig{{FunctionDeclarations: tools}}
	}

	return c.StreamGenerateContent(ctx, model, reqBody)
}

// ListModels returns available Gemini models that support generateContent or embedContent.
func (c *Client) ListModels() ([]string, error) {
	var allModels []struct {
		Name                       string   `json:"name"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	}

	pageToken := ""
	for {
		u := fmt.Sprintf("%s/models?key=%s&pageSize=1000", baseURL, c.apiKey)
		if pageToken != "" {
			u += "&pageToken=" + pageToken
		}

		resp, err := c.httpClient.Get(u)
		if err != nil {
			return nil, fmt.Errorf("list models: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list models returned %d: %s", resp.StatusCode, string(body))
		}

		var result struct {
			Models []struct {
				Name                       string   `json:"name"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse models: %w", err)
		}

		allModels = append(allModels, result.Models...)

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	var models []string
	for _, m := range allModels {
		name := m.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" && strings.HasPrefix(name, "gemini-") {
				models = append(models, name)
				break
			}
			if method == "embedContent" && strings.Contains(name, "embedding") {
				models = append(models, name)
				break
			}
		}
	}

	return models, nil
}
