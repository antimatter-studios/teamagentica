package catalog

import "strings"

// PricingEntry holds default pricing for a single model.
type PricingEntry struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
	CachedPer1M float64 `json:"cached_per_1m"`
	PerRequest  float64 `json:"per_request"`
	Currency    string  `json:"currency"`
}

// Entry represents a plugin available in the catalog.
type Entry struct {
	PluginID       string            `json:"plugin_id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Version        string            `json:"version"`
	Image          string            `json:"image"`
	Author         string            `json:"author"`
	Tags           []string          `json:"tags"`
	ConfigSchema   map[string]Field  `json:"config_schema,omitempty"`
	DefaultPricing []PricingEntry    `json:"default_pricing,omitempty"`
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field"`          // The key of the field to check
	Value string `json:"value"`          // Show this field when that field equals this value
}

// Field describes a config field in the schema.
type Field struct {
	Type        string       `json:"type"`
	Label       string       `json:"label"`
	Required    bool         `json:"required,omitempty"`
	Secret      bool         `json:"secret,omitempty"`
	Default     string       `json:"default,omitempty"`
	Options     []string     `json:"options,omitempty"`
	Dynamic     bool         `json:"dynamic,omitempty"`
	HelpText    string       `json:"help_text,omitempty"`
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`
	Order       int          `json:"order,omitempty"` // lower = shown first; 0 treated as unset
}

// OfficialCatalog is the hardcoded list of official plugins.
var OfficialCatalog = []Entry{
	{
		PluginID:    "agent-openai",
		Name:        "OpenAI Agent",
		Description: "AI agent powered by OpenAI GPT and Codex models with chat and completion capabilities",
		Version:     "1.0.0",
		Image:       "teamagentica-agent-openai:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "ai", "agent", "codex"},
		ConfigSchema: map[string]Field{
			"OPENAI_BACKEND": {Type: "select", Label: "Backend", Default: "subscription", Options: []string{"subscription", "api_key"}, HelpText: "Choose how to authenticate with OpenAI", Order: 1},
			"OPENAI_AUTH":    {Type: "oauth", Label: "Login with OpenAI", HelpText: "Authenticate with your OpenAI account to use Codex models", VisibleWhen: &VisibleWhen{Field: "OPENAI_BACKEND", Value: "subscription"}, Order: 2},
			"OPENAI_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.openai.com/api-keys", VisibleWhen: &VisibleWhen{Field: "OPENAI_BACKEND", Value: "api_key"}, Order: 2},
			"OPENAI_MODEL":        {Type: "select", Label: "Model", Default: "gpt-4o", Dynamic: true, Order: 3},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin. Each alias maps a short name to a plugin:model target.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "openai", Model: "gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00, CachedPer1M: 1.25, Currency: "USD"},
			{Provider: "openai", Model: "gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.075, Currency: "USD"},
			{Provider: "openai", Model: "o4-mini", InputPer1M: 1.10, OutputPer1M: 4.40, CachedPer1M: 0.275, Currency: "USD"},
			{Provider: "openai", Model: "gpt-5.1-codex", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.625, Currency: "USD"},
		},
	},
	{
		PluginID:    "discord-bot",
		Name:        "Discord Bot",
		Description: "Discord bot integration for receiving and responding to messages",
		Version:     "1.0.0",
		Image:       "teamagentica-discord:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "discord", "bot"},
		ConfigSchema: map[string]Field{
			"DISCORD_BOT_TOKEN": {Type: "string", Label: "Bot Token", Required: true, Secret: true, HelpText: "Discord bot token from developer portal", Order: 1},
			"PLUGIN_DEBUG":      {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
		},
	},
	{
		PluginID:    "telegram-bot",
		Name:        "Telegram Bot",
		Description: "Telegram bot integration for receiving and responding to messages",
		Version:     "1.0.0",
		Image:       "teamagentica-telegram:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "telegram", "bot"},
		ConfigSchema: map[string]Field{
			"TELEGRAM_BOT_TOKEN":     {Type: "string", Label: "Bot Token", Required: true, Secret: true, HelpText: "Telegram bot token from @BotFather", Order: 1},
			"TELEGRAM_MODE":          {Type: "select", Label: "Update Mode", Default: "poll", Options: []string{"poll", "webhook"}, HelpText: "How the bot receives messages from Telegram", Order: 2},
			"TELEGRAM_POLL_TIMEOUT":  {Type: "number", Label: "Poll Timeout (seconds)", Default: "60", HelpText: "Long poll timeout — Telegram holds the connection open for this many seconds waiting for new messages", VisibleWhen: &VisibleWhen{Field: "TELEGRAM_MODE", Value: "poll"}, Order: 3},
			"TELEGRAM_WEBHOOK_URL":   {Type: "string", Label: "Webhook URL", HelpText: "Public HTTPS URL that Telegram will POST updates to", VisibleWhen: &VisibleWhen{Field: "TELEGRAM_MODE", Value: "webhook"}, Order: 3},
			"TELEGRAM_ALLOWED_USERS": {Type: "string", Label: "Allowed User IDs", HelpText: "Comma-separated Telegram user IDs. Leave empty to allow all users.", Order: 4},
			"DEFAULT_AGENT":          {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator. Leave empty to require @mention routing.", Order: 5},
			"PLUGIN_DEBUG":           {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
		},
	},
	{
		PluginID:    "whatsapp-bot",
		Name:        "WhatsApp Bot",
		Description: "WhatsApp bot integration using the WhatsApp Business Cloud API for receiving and responding to messages",
		Version:     "1.0.0",
		Image:       "teamagentica-whatsapp:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "whatsapp", "bot", "messaging"},
		ConfigSchema: map[string]Field{
			"WHATSAPP_ACCESS_TOKEN":    {Type: "string", Label: "Access Token", Required: true, Secret: true, HelpText: "Permanent access token from Meta developer portal", Order: 1},
			"WHATSAPP_PHONE_NUMBER_ID": {Type: "string", Label: "Phone Number ID", Required: true, HelpText: "WhatsApp Business phone number ID from Meta developer portal", Order: 2},
			"WHATSAPP_VERIFY_TOKEN":    {Type: "string", Label: "Webhook Verify Token", Required: true, HelpText: "A secret string you choose — must match what you enter in Meta's webhook configuration", Order: 3},
			"WHATSAPP_APP_SECRET":      {Type: "string", Label: "App Secret", Secret: true, HelpText: "Optional app secret for webhook signature verification", Order: 4},
			"PLUGIN_DEBUG":             {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	},
	{
		PluginID:    "ngrok",
		Name:        "ngrok Tunnel",
		Description: "Creates an ngrok tunnel for exposing plugin services to the internet. Publishes tunnel URL events for other plugins to consume.",
		Version:     "1.0.0",
		Image:       "teamagentica-ngrok:latest",
		Author:      "TeamAgentica",
		Tags:        []string{"tunnel", "networking", "ngrok", "webhook"},
		ConfigSchema: map[string]Field{
			"NGROK_AUTHTOKEN":    {Type: "string", Label: "ngrok Auth Token", Required: true, Secret: true, HelpText: "Your ngrok authentication token from https://dashboard.ngrok.com"},
			"NGROK_DOMAIN":       {Type: "string", Label: "Custom Domain", HelpText: "Optional static ngrok domain (e.g. my-app.ngrok-free.app). Leave empty for a random URL."},
			"NGROK_TUNNEL_TARGET": {Type: "string", Label: "Tunnel Target", HelpText: "Internal host:port to tunnel to. Leave empty to use the kernel. Set to webhook-ingress host:port if using the webhook ingress plugin (e.g. teamagentica-plugin-webhook-ingress:9000)."},
		},
	},
	{
		PluginID:    "webhook-ingress",
		Name:        "Webhook Ingress",
		Description: "Public-facing HTTP server that routes external webhook traffic to plugins. Keeps the kernel off the public internet. Works with ngrok for automatic URL discovery.",
		Version:     "1.0.0",
		Image:       "teamagentica-webhook-ingress:latest",
		Author:      "TeamAgentica",
		Tags:        []string{"webhook", "ingress", "networking", "routing"},
		ConfigSchema: map[string]Field{
			"WEBHOOK_INGRESS_PORT": {Type: "number", Label: "Listen Port", Default: "9000", HelpText: "Port the ingress listens on for external webhook traffic"},
		},
	},
	{
		PluginID:    "agent-gemini",
		Name:        "Google Gemini Agent",
		Description: "AI agent powered by Google Gemini models including Flash and Pro",
		Version:     "1.0.0",
		Image:       "teamagentica-agent-gemini:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "ai", "agent", "gemini", "google"},
		ConfigSchema: map[string]Field{
			"GEMINI_API_KEY":      {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://aistudio.google.com/apikey", Order: 1},
			"GEMINI_MODEL":        {Type: "select", Label: "Model", Default: "gemini-2.5-flash", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "gemini", Model: "gemini-2.5-flash", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
			{Provider: "gemini", Model: "gemini-2.5-pro", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
			{Provider: "gemini", Model: "gemini-2.0-flash", InputPer1M: 0.10, OutputPer1M: 0.40, CachedPer1M: 0.025, Currency: "USD"},
		},
	},
	{
		PluginID:    "tool-veo",
		Name:        "Google Veo Video",
		Description: "AI video generation tool powered by Google Veo. Generate videos from text prompts via the Gemini API.",
		Version:     "1.0.0",
		Image:       "teamagentica-tool-veo:latest",
		Author:      "teamagentica",
		Tags:        []string{"video", "ai", "tool", "veo", "google", "gemini"},
		ConfigSchema: map[string]Field{
			"GEMINI_API_KEY":      {Type: "string", Label: "Gemini API Key", Required: true, Secret: true, HelpText: "Get your API key at https://aistudio.google.com/apikey", Order: 1},
			"VEO_MODEL":           {Type: "select", Label: "Model", Default: "veo-3.1-generate-preview", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "veo", Model: "veo-3.1-generate-preview", PerRequest: 0.025, Currency: "USD"},
		},
	},
	{
		PluginID:    "tool-seedance",
		Name:        "Seedance Video",
		Description: "AI video generation tool powered by ByteDance Seedance. Generate videos from text prompts.",
		Version:     "1.0.0",
		Image:       "teamagentica-tool-seedance:latest",
		Author:      "teamagentica",
		Tags:        []string{"video", "ai", "tool", "seedance", "bytedance"},
		ConfigSchema: map[string]Field{
			"SEEDANCE_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key from the Dreamina developer portal", Order: 1},
			"SEEDANCE_MODEL":   {Type: "select", Label: "Model", Default: "seedance-2.0", Dynamic: true, Order: 2},
			"PLUGIN_DEBUG":     {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "seedance", Model: "seedance-2.0", PerRequest: 0.03, Currency: "USD"},
		},
	},
	{
		PluginID:    "agent-openrouter",
		Name:        "OpenRouter",
		Description: "AI router providing access to hundreds of models from OpenAI, Anthropic, Google, Meta and more via OpenRouter",
		Version:     "1.0.0",
		Image:       "teamagentica-agent-openrouter:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "ai", "agent", "openrouter", "router"},
		ConfigSchema: map[string]Field{
			"OPENROUTER_API_KEY":  {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://openrouter.ai/keys", Order: 1},
			"OPENROUTER_MODEL":    {Type: "select", Label: "Model", Default: "google/gemini-2.5-flash", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "openrouter", Model: "google/gemini-2.5-flash", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
			{Provider: "openrouter", Model: "openai/gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00, CachedPer1M: 1.25, Currency: "USD"},
			{Provider: "openrouter", Model: "anthropic/claude-sonnet-4", InputPer1M: 3.00, OutputPer1M: 15.00, CachedPer1M: 0.30, Currency: "USD"},
			{Provider: "openrouter", Model: "google/gemini-2.5-pro", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
			{Provider: "openrouter", Model: "meta-llama/llama-4-maverick", InputPer1M: 0.50, OutputPer1M: 1.50, Currency: "USD"},
		},
	},
	{
		PluginID:    "agent-requesty",
		Name:        "Requesty Router",
		Description: "AI router providing unified access to OpenAI, Anthropic, Google and more via Requesty",
		Version:     "1.0.0",
		Image:       "teamagentica-agent-requesty:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "ai", "agent", "requesty", "router"},
		ConfigSchema: map[string]Field{
			"REQUESTY_API_KEY":    {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://app.requesty.ai", Order: 1},
			"REQUESTY_MODEL":      {Type: "select", Label: "Model", Default: "google/gemini-2.5-flash", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "requesty", Model: "google/gemini-2.5-flash", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
			{Provider: "requesty", Model: "openai/gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00, CachedPer1M: 1.25, Currency: "USD"},
			{Provider: "requesty", Model: "anthropic/claude-sonnet-4-20250514", InputPer1M: 3.00, OutputPer1M: 15.00, CachedPer1M: 0.30, Currency: "USD"},
			{Provider: "requesty", Model: "google/gemini-2.5-pro", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
			{Provider: "requesty", Model: "openai/gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.075, Currency: "USD"},
		},
	},
	{
		PluginID:    "tool-nanobanana",
		Name:        "Nano Banana Image",
		Description: "AI image generation using Google Gemini's native image output. Uses your existing Gemini API key.",
		Version:     "1.0.0",
		Image:       "teamagentica-tool-nanobanana:latest",
		Author:      "teamagentica",
		Tags:        []string{"image", "ai", "tool", "gemini", "google"},
		ConfigSchema: map[string]Field{
			"GEMINI_API_KEY":      {Type: "string", Label: "Gemini API Key", Required: true, Secret: true, HelpText: "Get your API key at https://aistudio.google.com/apikey", Order: 1},
			"NANOBANANA_MODEL":    {Type: "select", Label: "Model", Default: "gemini-2.5-flash-image", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "nanobanana", Model: "gemini-2.5-flash-image", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
			{Provider: "nanobanana", Model: "gemini-3.1-flash-image-preview", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
			{Provider: "nanobanana", Model: "gemini-3-pro-image-preview", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
		},
	},
	{
		PluginID:    "agent-kimi",
		Name:        "Kimi K2 Agent",
		Description: "AI agent powered by Moonshot's Kimi K2 with 128K context and thinking mode",
		Version:     "1.0.0",
		Image:       "teamagentica-agent-kimi:latest",
		Author:      "teamagentica",
		Tags:        []string{"chat", "ai", "agent", "kimi", "moonshot"},
		ConfigSchema: map[string]Field{
			"KIMI_API_KEY":        {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.moonshot.ai", Order: 1},
			"KIMI_MODEL":          {Type: "select", Label: "Model", Default: "kimi-k2-turbo-preview", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "moonshot", Model: "kimi-k2-turbo-preview", InputPer1M: 1.15, OutputPer1M: 8.00, CachedPer1M: 0.29, Currency: "USD"},
			{Provider: "moonshot", Model: "kimi-k2.5", InputPer1M: 0.60, OutputPer1M: 3.00, CachedPer1M: 0.15, Currency: "USD"},
			{Provider: "moonshot", Model: "kimi-k2-0905-preview", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
			{Provider: "moonshot", Model: "kimi-k2-0711-preview", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
			{Provider: "moonshot", Model: "kimi-k2-thinking", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
			{Provider: "moonshot", Model: "kimi-k2-thinking-turbo", InputPer1M: 1.15, OutputPer1M: 8.00, CachedPer1M: 0.29, Currency: "USD"},
		},
	},
	{
		PluginID:    "tool-stability",
		Name:        "Stability AI Image",
		Description: "AI image generation powered by Stable Diffusion 3. Generate images from text prompts with negative prompts and aspect ratio control.",
		Version:     "1.0.0",
		Image:       "teamagentica-tool-stability:latest",
		Author:      "teamagentica",
		Tags:        []string{"image", "ai", "tool", "stability", "stable-diffusion"},
		ConfigSchema: map[string]Field{
			"STABILITY_API_KEY":   {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.stability.ai — 25 free credits on signup", Order: 1},
			"STABILITY_MODEL":     {Type: "select", Label: "Model", Default: "sd3-medium", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":      {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":        {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
		DefaultPricing: []PricingEntry{
			{Provider: "stability", Model: "sd3-medium", PerRequest: 0.035, Currency: "USD"},
			{Provider: "stability", Model: "sd3-large", PerRequest: 0.065, Currency: "USD"},
			{Provider: "stability", Model: "sd3-large-turbo", PerRequest: 0.04, Currency: "USD"},
		},
	},
	{
		PluginID:    "sss3-storage",
		Name:        "S3 Storage",
		Description: "S3-compatible object storage powered by stupid-simple-s3. Upload, download, and browse files with an in-memory metadata cache for fast listing.",
		Version:     "1.0.0",
		Image:       "teamagentica-sss3-storage:latest",
		Author:      "teamagentica",
		Tags:        []string{"storage", "s3", "files", "media", "tool"},
		ConfigSchema: map[string]Field{
			"S3_ENDPOINT":   {Type: "string", Label: "S3 Endpoint", Required: true, Default: "http://sss3:9000", HelpText: "S3-compatible endpoint URL", Order: 1},
			"S3_BUCKET":     {Type: "string", Label: "Bucket Name", Required: true, Default: "teamagentica", HelpText: "S3 bucket name for storage", Order: 2},
			"S3_ACCESS_KEY": {Type: "string", Label: "Access Key", Required: true, Secret: true, Default: "minioadmin", HelpText: "S3 access key", Order: 3},
			"S3_SECRET_KEY": {Type: "string", Label: "Secret Key", Required: true, Secret: true, Default: "minioadmin", HelpText: "S3 secret key", Order: 4},
			"S3_REGION":     {Type: "string", Label: "Region", Default: "us-east-1", HelpText: "S3 region", Order: 5},
			"PLUGIN_DEBUG":  {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed S3 operations", Order: 99},
		},
	},
	{
		PluginID:    "chat",
		Name:        "Web Chat",
		Description: "Built-in web chat interface for conversing with AI agents. Supports file uploads, conversation history, and coordinator routing.",
		Version:     "1.0.0",
		Image:       "teamagentica-chat:latest",
		Author:      "teamagentica",
		Tags:        []string{"system", "chat", "ui"},
		ConfigSchema: map[string]Field{
			"DEFAULT_AGENT": {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator. Leave empty to require manual agent selection.", Order: 1},
			"PLUGIN_DEBUG":  {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	},
	{
		PluginID:    "mcp-server",
		Name:        "MCP Server",
		Description: "Model Context Protocol server that exposes platform tools and agent routing to all AI agents via MCP",
		Version:     "1.0.0",
		Image:       "teamagentica-mcp-server:latest",
		Author:      "teamagentica",
		Tags:        []string{"infrastructure", "mcp", "tools", "routing"},
		ConfigSchema: map[string]Field{
			"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed MCP protocol traffic", Order: 99},
		},
	},
	{
		PluginID:    "cost-explorer",
		Name:        "Cost Explorer",
		Description: "Centralized usage tracking and cost analytics for all AI agent and tool plugins",
		Version:     "1.0.0",
		Image:       "teamagentica-cost-explorer:latest",
		Author:      "teamagentica",
		Tags:        []string{"system", "costs", "usage", "analytics"},
		ConfigSchema: map[string]Field{
			"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Enable debug logging", Order: 99},
		},
	},
}

// Search filters the catalog by query string (matches against ID, name, description, tags).
func Search(q string) []Entry {
	if q == "" {
		return OfficialCatalog
	}

	q = strings.ToLower(q)
	var results []Entry
	for _, e := range OfficialCatalog {
		if matches(e, q) {
			results = append(results, e)
		}
	}
	return results
}

func matches(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.PluginID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), q) {
		return true
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}
