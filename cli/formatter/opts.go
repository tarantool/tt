package formatter

// Opts contains formatting options.
type Opts struct {
	// Graphics sets on/off the output of pseudographic characters.
	Graphics bool
	// ColumnWidthMax sets is a maximum width of columns.
	ColumnWidthMax int
	// TableDialect sets a current table dialect.
	TableDialect TableDialect
}
