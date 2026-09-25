package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
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
	// Logger receives the adapter's structured operation logs; nil disables it.
	Logger *slog.Logger
}

// Search reports every rock whose name contains the term, across the
// configured servers.
//
// A search reports what exists rather than picking one artifact, so every
// server is consulted and a match from each is kept - unlike resolution, which
// stops at the first server that has the rock. A term that matches nothing is
// an empty result and no error: "no such rock" is an answer, not a failure.
func Search(ctx context.Context, opts SearchOptions) ([]Match, error) {
	adapter := adapterFor(opts.Tarantool, opts.Registries, opts.WorkingDir, opts.Logger)

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

// SearchResult is what tt package search reports: the matches for a term.
//
// The machine formats encode the matches alone, as a list, so no match is an
// empty list - a consumer parsing the output gets a valid empty document
// rather than an empty file. The term is there for the human form, which
// writes nothing to stdout on a miss and logs a note naming the term instead,
// so a redirected table is the table and nothing else.
type SearchResult struct {
	// Term is the substring that was searched for.
	Term string
	// Matches are the rocks found, in the order the servers reported them.
	Matches []Match
}

// MarshalJSON encodes the matches as a JSON list.
func (r SearchResult) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(r.Matches)
	if err != nil {
		return nil, fmt.Errorf("encoding matches: %w", err)
	}

	return data, nil
}

// MarshalYAML encodes the matches as a YAML list.
func (r SearchResult) MarshalYAML() (any, error) {
	return r.Matches, nil
}

// Human writes the matches as a table, or logs that nothing matched.
func (r SearchResult) Human(out io.Writer) error {
	return r.human(out, log.Logger())
}

// human is Human with the logger the miss is noted on.
func (r SearchResult) human(out io.Writer, logger *slog.Logger) error {
	if len(r.Matches) == 0 {
		logger.Info(fmt.Sprintf("no rock matches %q", r.Term))

		return nil
	}

	rows := make([]string, 0, len(r.Matches))
	for _, match := range r.Matches {
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s", match.Name, match.Version, match.Server))
	}

	return renderRows(out, "NAME\tVERSION\tSERVER", rows)
}

// adapterFor builds the rocks adapter bound to the effective server list,
// logging its operations to logger (nil disables it).
func adapterFor(
	tnt rocks.TarantoolInfo, registries []rocks.Registry, workingDir string, logger *slog.Logger,
) *rocks.Adapter {
	return rocks.New(rocks.BuildConfig(tnt, rocks.ConfigOptions{
		Tree:       filepath.Join(workingDir, rocksDirName),
		WorkingDir: workingDir,
		Servers:    rocks.URLs(registries),
		Logger:     logger,
	}))
}
