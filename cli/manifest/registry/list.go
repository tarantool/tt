package registry

import (
	"fmt"
	"io"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// Servers is the effective server list as tt registry list reports it, in
// the order the servers are queried. The machine formats encode it as a list
// of url/source pairs.
type Servers []rocks.Registry

// Human writes the human-readable list: the servers in the order they are
// queried, each with the configuration layer it came from.
//
// The list is always non-empty - with nothing configured it is the built-in
// defaults - so there is no "nothing found" case to word.
func (s Servers) Human(out io.Writer) error {
	rows := make([]string, 0, len(s))
	for _, registry := range s {
		rows = append(rows, fmt.Sprintf("%s\t%s", registry.URL, registry.Source))
	}

	return renderRows(out, "URL\tSOURCE", rows)
}
