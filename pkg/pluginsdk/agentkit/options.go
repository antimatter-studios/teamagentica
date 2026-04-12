package agentkit

// Config holds runtime configuration for the agent handler.
type Config struct {
	DefaultModel string
	MaxTokens    int
	Temperature  float64
	MaxToolLoops int  // prevent infinite tool loops, default 10
	Debug        bool
}

func defaultConfig() Config {
	return Config{
		MaxTokens:    4096,
		Temperature:  0.7,
		MaxToolLoops: 10,
	}
}

// Option configures the agent runtime.
type Option func(*Config)

// WithDefaultModel sets the default model when the request doesn't specify one.
func WithDefaultModel(model string) Option {
	return func(c *Config) { c.DefaultModel = model }
}

// WithMaxTokens sets the maximum tokens for LLM responses.
func WithMaxTokens(n int) Option {
	return func(c *Config) { c.MaxTokens = n }
}

// WithTemperature sets the default temperature.
func WithTemperature(t float64) Option {
	return func(c *Config) { c.Temperature = t }
}

// WithMaxToolLoops sets the maximum number of tool-use round trips.
func WithMaxToolLoops(n int) Option {
	return func(c *Config) { c.MaxToolLoops = n }
}

// WithDebug enables verbose event logging.
func WithDebug(d bool) Option {
	return func(c *Config) { c.Debug = d }
}
