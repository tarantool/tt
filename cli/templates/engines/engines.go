// Package engines provides template engine interface and implementations.
package engines

// TemplateEngine is an interface to support to use for application template instantiation.
type TemplateEngine interface {
	// RenderFile applies data to the template from srcPath.
	// Instantiated template is saved as dstPath.
	RenderFile(srcPath string, dstPath string, data interface{}) error

	// RenderFile applies data to the template text. Returns instantiated text.
	RenderText(in string, data interface{}) (string, error)
}

// NewDefaultEngine creates and returns default template engine.
func NewDefaultEngine() TemplateEngine {
	return goTextEngine{}
}
