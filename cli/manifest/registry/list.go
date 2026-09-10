package registry

import (
	"fmt"
	"io"

	"github.com/tarantool/tt/cli/manifest/rocks"
)

// RenderList writes the effective server list in the chosen format.
//
// The list is always non-empty - with nothing configured it is the built-in
// defaults - so there is no "nothing found" case to word.
func RenderList(out io.Writer, registries []rocks.Registry, format Format) error {
	switch format {
	case FormatJSON:
		return renderJSON(out, registries)
	case FormatYAML:
		return renderYAML(out, registries)
	case FormatTable:
		return renderListTable(out, registries)
	default:
		return stateErrorf("%w %q", ErrUnknownFormat, format)
	}
}

// renderListTable writes the human-readable list: the servers in the order
// they are queried, each with the configuration layer it came from.
func renderListTable(out io.Writer, registries []rocks.Registry) error {
	rows := make([]string, 0, len(registries))
	for _, registry := range registries {
		rows = append(rows, fmt.Sprintf("%s\t%s", registry.URL, registry.Source))
	}

	return renderRows(out, "URL\tSOURCE", rows)
}
