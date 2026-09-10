package registry

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest/rocks"
)

// rocksDirName is the tree the adapter is bound to. Neither search nor
// download reads or writes it - both work against remote servers and the
// download directory - but the engine configuration requires a tree, and the
// project's own is the one that would be meant if anything ever did.
const rocksDirName = ".rocks"

// Match is one rock offered by one server, as a search reports it.
type Match struct {
	// Name is the rock name.
	Name string `json:"name" yaml:"name"`
	// Version is the version-revision string.
	Version string `json:"version" yaml:"version"`
	// Server is the server the offering came from.
	Server string `json:"server" yaml:"server"`
}

// SearchOptions configures one search.
type SearchOptions struct {
	// Term is the substring to look for in rock names.
	Term string
	// Registries is the effective server list, queried in order.
	Registries []rocks.Registry
	// Tarantool carries the Tarantool facts the engine configuration needs.
	Tarantool rocks.TarantoolInfo
	// WorkingDir is the directory the engine resolves relative paths against.
	WorkingDir string
}

// Search reports every rock whose name contains the term, across the
// configured servers.
//
// A search reports what exists rather than picking one artifact, so every
// server is consulted and a match from each is kept - unlike resolution, which
// stops at the first server that has the rock. A term that matches nothing is
// an empty result and no error: "no such rock" is an answer, not a failure.
func Search(ctx context.Context, opts SearchOptions) ([]Match, error) {
	adapter := adapterFor(opts.Tarantool, opts.Registries, opts.WorkingDir)

	results, err := adapter.Search(ctx, opts.Term, client.SearchOpts{
		Version: "",
		Source:  false,
		Binary:  false,
		All:     false,
		// The effective list is already the adapter's configured list;
		// naming it here again would query every server twice, because
		// SearchOpts.Servers prepends rather than replaces.
		Servers: nil,
	})
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	matches := make([]Match, 0, len(results))
	for _, result := range results {
		matches = append(matches, Match{
			Name:    result.Name,
			Version: result.Version,
			Server:  result.Server,
		})
	}

	return matches, nil
}

// RenderSearch writes the matches in the chosen format. out carries the
// listing, notes the narrative, so a redirected table is the table and nothing
// else.
//
// No match writes nothing to out in every format but the machine ones, which
// write an empty document: a consumer parsing the output must get a valid
// empty list rather than an empty file.
func RenderSearch(out, notes io.Writer, matches []Match, term string, format Format) error {
	switch format {
	case FormatJSON:
		return renderJSON(out, matches)
	case FormatYAML:
		return renderYAML(out, matches)
	case FormatTable:
		return renderSearchTable(out, notes, matches, term)
	default:
		return stateErrorf("%w %q", ErrUnknownFormat, format)
	}
}

// renderSearchTable writes the human-readable listing, or a note on notes when
// nothing matched.
func renderSearchTable(out, notes io.Writer, matches []Match, term string) error {
	if len(matches) == 0 {
		_, err := fmt.Fprintf(notes, "no rock matches %q\n", term)
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}

		return nil
	}

	rows := make([]string, 0, len(matches))
	for _, match := range matches {
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s", match.Name, match.Version, match.Server))
	}

	return renderRows(out, "NAME\tVERSION\tSERVER", rows)
}

// adapterFor builds the rocks adapter bound to the effective server list.
func adapterFor(
	tnt rocks.TarantoolInfo, registries []rocks.Registry, workingDir string,
) *rocks.Adapter {
	return rocks.New(rocks.BuildConfig(tnt, rocks.ConfigOptions{
		Tree:       filepath.Join(workingDir, rocksDirName),
		WorkingDir: workingDir,
		Servers:    rocks.URLs(registries),
		Logger:     nil,
	}))
}
