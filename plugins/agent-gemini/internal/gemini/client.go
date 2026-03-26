package gemini

import (
	"bytes"
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
	Role             string                 `json:"role"`
	Content          string                 `json:"content"`
	FunctionCallName string                 `json:"-"`
	FunctionCallArgs map[string]interface{} `json:"-"`
	FunctionRespName string                 `json:"-"`
	FunctionRespData map[string]interface{} `json:"-"`
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
	Content      string
	FunctionCall *FunctionCall
	Usage        Usage
	Headers      http.Header
}

// part is a piece of content in the Gemini API.
type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
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
				Text         string        `json:"text"`
				FunctionCall *FunctionCall `json:"functionCall,omitempty"`
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
			}
		}
	}

	return &ChatResponse{
		Content:      responseText,
		FunctionCall: funcCall,
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
