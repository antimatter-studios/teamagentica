package render

// Fields is a convenience alias for item data.
type Fields = map[string]string

// Renderer defines how tacli outputs data.
type Renderer interface {
	// Header outputs a title block (text mode only, ignored in JSON).
	Header(title, subtitle, version string)

	// GroupStart begins a named group of items.
	GroupStart(name string)

	// Item outputs a single row of key-value data.
	Item(fields map[string]string)

	// Flush finalises output (JSON mode writes the accumulated buffer).
	Flush() error
}
