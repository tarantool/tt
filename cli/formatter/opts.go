package formatter

// Opts contains formatting options.
type Opts struct {
	// Graphics sets on/off the output of pseudographics characters.
	Graphics bool
	// ColumnWidthMax sets is a maximum width of columns.
	ColumnWidthMax int
	// TableDialect sets a current table dialect.
	TableDialect TableDialect
}
