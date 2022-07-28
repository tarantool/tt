package engines

type TemplateEngine interface {
	RenderFile(srcPath string, dstPath string, data interface{}) error

	RenderText(in string, data interface{}) (string, error)
}
