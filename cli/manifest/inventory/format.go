package inventory

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// yamlIndent is tt's usual YAML indent, and tabColumnPadding the gap between
// table columns.
const (
	yamlIndent       = 2
	tabColumnPadding = 2
)

// Format is how a listing is rendered.
type Format string

const (
	// FormatTable is the human-readable column layout.
	FormatTable Format = "table"
	// FormatJSON is machine-readable JSON.
	FormatJSON Format = "json"
	// FormatYAML is machine-readable YAML.
	FormatYAML Format = "yaml"
)

// ParseFormat resolves the -o value against whether stdout is a terminal.
//
// An explicit value always wins. With none given the default follows the tt
// convention: a terminal gets the table, and anything else — a pipe, a file, CI
// — gets YAML, so a command whose output is being consumed produces something
// parseable without the caller having to remember a flag.
func ParseFormat(raw string, tty bool) (Format, error) {
	switch Format(raw) {
	case "":
		if tty {
			return FormatTable, nil
		}

		return FormatYAML, nil
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatYAML:
		return FormatYAML, nil
	default:
		return "", stateErrorf("%w %q (want table, json or yaml)", ErrUnknownFormat, raw)
	}
}

// Render writes a listing in the chosen format.
//
// The human format has two shapes, chosen by the listing rather than by a
// separate argument: a listing carrying the dependency index renders as a tree,
// a plain one as a flat table. The machine formats need no such split — the
// index is simply another key when it is present.
func Render(out io.Writer, listing *Listing, format Format) error {
	switch format {
	case FormatJSON:
		return renderJSON(out, listing)
	case FormatYAML:
		return renderYAML(out, listing)
	case FormatTable:
		if listing.Rocks != nil {
			return renderTree(out, listing)
		}

		return renderTable(out, listing)
	default:
		return stateErrorf("%w %q", ErrUnknownFormat, format)
	}
}

// renderJSON writes indented JSON with a trailing newline, so the output is
// pleasant both piped into jq and read directly.
func renderJSON(out io.Writer, listing *Listing) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(listing)
	if err != nil {
		return fmt.Errorf("rendering JSON: %w", err)
	}

	return nil
}

// renderYAML writes YAML at tt's usual two-space indent.
func renderYAML(out io.Writer, listing *Listing) error {
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(listing)
	if err != nil {
		return fmt.Errorf("rendering YAML: %w", err)
	}

	return encoder.Close() //nolint:wrapcheck // Close reports the same encode error.
}

// renderTable writes the human-readable listing: one row per package, with the
// primary package marked so it is obvious which one uninstall will refuse.
//
// An empty scope prints a sentence rather than a bare header — "nothing is
// installed" is the answer, and a lone header row reads like a bug.
func renderTable(out io.Writer, listing *Listing) error {
	if len(listing.Packages) == 0 {
		_, err := fmt.Fprintf(out, "no packages installed in %s scope (%s)\n",
			listing.Scope, listing.Root)
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}

		return nil
	}

	table := tabwriter.NewWriter(out, 0, 0, tabColumnPadding, ' ', 0)

	_, err := fmt.Fprintln(table, "NAME\tVERSION\tORIGIN\tDESCRIPTION")
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	for _, entry := range listing.Packages {
		_, err = fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			entry.Name, dash(entry.Version), originOf(entry), oneLine(entry.Description))
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}
	}

	err = table.Flush()
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	return nil
}

// Tree drawing. A child that closes its level uses the corner, everything else
// the tee; the continuation keeps deeper levels connected to the branch above.
const (
	treeBranch       = "\u251c\u2500\u2500 "
	treeLast         = "\u2514\u2500\u2500 "
	treeContinuation = "\u2502   "
	treeIndent       = "    "
	// transitiveLabel heads the rocks that are in a package's closure but that no
	// declared rock reaches through the tree manifest's edges — a tree with no
	// manifest at all, or one whose edges are incomplete. They are shown without
	// a parent rather than dropped or attached to a guess.
	transitiveLabel = "pulled in transitively (parent not recorded)"
	// noDepsLabel stands in for an empty child list, so a leaf never renders as a
	// bare header that looks truncated.
	noDepsLabel = "(no dependencies)"
)

// treeNode is one rendered line with whatever hangs off it.
type treeNode struct {
	label    string
	children []treeNode
}

// renderTree writes the dependency view as one tree.
//
// The root is the project's own package: guests are installed into its tree and
// share its .rocks/, so the project is what contains them even though it does
// not depend on them — hence the (guest) marker rather than presenting them as
// requirements. A scope with no primary package has nothing to root at, so the
// scope itself becomes the root and every package hangs off it.
//
// The annotation on each rock is the point of the view. Before an uninstall the
// useful question is not what a package depends on but what would leave with it,
// so every rock says which side it falls on.
func renderTree(out io.Writer, listing *Listing) error {
	if len(listing.Packages) == 0 {
		return renderTable(out, listing)
	}

	root := buildTree(listing)

	_, err := fmt.Fprintf(out, "%s scope \u2014 %s\n\n%s\n",
		listing.Scope, listing.Root, root.label)
	if err != nil {
		return fmt.Errorf("rendering tree: %w", err)
	}

	return renderChildren(out, root.children, "")
}

// buildTree assembles the single-rooted node tree: the primary package with its
// own rocks, then every guest as a subtree beneath it.
func buildTree(listing *Listing) treeNode {
	index := make(map[string]RockEntry, len(listing.Rocks))
	for _, dep := range listing.Rocks {
		index[dep.Name] = dep
	}

	var (
		root   treeNode
		guests []Entry
		rooted bool
	)

	for _, entry := range listing.Packages {
		if entry.Primary && !rooted {
			root = treeNode{
				label:    packageLabel(entry) + " (primary)",
				children: dependencyNodes(entry, index),
			}
			rooted = true

			continue
		}

		guests = append(guests, entry)
	}

	if !rooted {
		// No project package to root at — a shared tree, or a deploy directory
		// holding guests only.
		root = treeNode{label: "(no project package)", children: nil}
	}

	for _, guest := range guests {
		root.children = append(root.children, treeNode{
			label:    packageLabel(guest) + " (guest)",
			children: orNoDeps(dependencyNodes(guest, index)),
		})
	}

	root.children = orNoDeps(root.children)

	return root
}

// renderChildren writes one level and recurses, extending the prefix so deeper
// levels stay visually attached to the branch that carries them.
func renderChildren(out io.Writer, children []treeNode, prefix string) error {
	for i, child := range children {
		last := i == len(children)-1

		_, err := fmt.Fprintf(out, "%s%s%s\n", prefix, branchOf(last), child.label)
		if err != nil {
			return fmt.Errorf("rendering tree: %w", err)
		}

		err = renderChildren(out, child.children, prefix+continuationOf(last))
		if err != nil {
			return err
		}
	}

	return nil
}

// dependencyNodes turns a package's closure into its branches.
//
// The rocks the manifest declared hang off the package, and each of those
// carries whatever it requires in turn, walked through the edges LuaRocks
// records in the tree manifest. A rock in the closure that no walk reaches —
// because the tree carries no manifest, or its edges are incomplete — is still
// listed, under a group that admits its parent is unknown rather than dropping
// it or guessing.
func dependencyNodes(entry Entry, index map[string]RockEntry) []treeNode {
	direct, _ := partitionDepth(entry)

	// A rock is only nested under a parent if this package actually brought it;
	// the tree manifest is scope-wide and knows about rocks other packages own.
	closure := entry.Dependencies

	reached := map[string]struct{}{}
	nodes := make([]treeNode, 0, len(direct)+1)

	for _, name := range direct {
		nodes = append(nodes, requirementNode(entry, name, index, closure, reached, nil))
	}

	// Whatever the walk did not reach: an unrooted remainder, kept visible.
	var orphans []string

	for _, name := range sortedKeys(closure) {
		if _, seen := reached[name]; !seen {
			orphans = append(orphans, name)
		}
	}

	if len(orphans) == 0 {
		return nodes
	}

	group := treeNode{label: transitiveLabel, children: make([]treeNode, 0, len(orphans))}

	for _, name := range orphans {
		group.children = append(group.children,
			requirementNode(entry, name, index, closure, reached, nil))
	}

	return append(nodes, group)
}

// requirementNode builds one rock's branch and, beneath it, the rocks it
// requires.
//
// path carries the ancestors of this node so a dependency cycle terminates
// instead of recursing forever. LuaRocks closures are acyclic in principle, but
// the tree manifest is data on disk and a view must not hang on a malformed one.
func requirementNode(
	entry Entry, name string, index map[string]RockEntry,
	closure map[string]string, reached map[string]struct{}, path []string,
) treeNode {
	reached[name] = struct{}{}

	node := treeNode{label: depLabel(entry, name, index), children: nil}

	for _, required := range index[name].Requires {
		// Only rocks this package brought, and never one already on the path
		// above us.
		if _, inClosure := closure[required]; !inClosure {
			continue
		}

		if slices.Contains(path, required) || required == name {
			continue
		}

		node.children = append(node.children,
			requirementNode(entry, required, index, closure, reached, append(path, name)))
	}

	return node
}

// partitionDepth splits a package's closure into what its manifest declared and
// what came in behind those declarations, each in name order.
func partitionDepth(entry Entry) ([]string, []string) {
	declared := make(map[string]struct{}, len(entry.Direct))
	for _, name := range entry.Direct {
		declared[name] = struct{}{}
	}

	var direct, transitive []string

	for _, name := range sortedKeys(entry.Dependencies) {
		if _, ok := declared[name]; ok {
			direct = append(direct, name)
		} else {
			transitive = append(transitive, name)
		}
	}

	return direct, transitive
}

// orNoDeps substitutes the placeholder for an empty child list.
func orNoDeps(children []treeNode) []treeNode {
	if len(children) > 0 {
		return children
	}

	return []treeNode{{label: noDepsLabel, children: nil}}
}

// packageLabel names a package and its version.
func packageLabel(entry Entry) string {
	if entry.Version == "" {
		return entry.Name
	}

	return entry.Name + " " + entry.Version
}

// depLabel names a rock, the version in the tree, and what would become of it.
func depLabel(entry Entry, name string, index map[string]RockEntry) string {
	return name + " " + entry.Dependencies[name] + shareNote(entry.Name, index[name])
}

// branchOf picks the tee or the closing corner.
func branchOf(last bool) string {
	if last {
		return treeLast
	}

	return treeBranch
}

// continuationOf is the prefix a child's own children inherit: blank once the
// branch has closed, a vertical bar while siblings still follow.
func continuationOf(last bool) string {
	if last {
		return treeIndent
	}

	return treeContinuation
}

// shareNote says what would happen to a rock if the package holding this branch
// were uninstalled: nothing when someone else holds it, removal when nobody
// does.
//
// A rock that is itself an installed package is neither — it survives on its own
// metadata, not on anyone's reference — so it is labelled as what it is.
func shareNote(owner string, dep RockEntry) string {
	if dep.Installed {
		return " (installed package)"
	}

	others := make([]string, 0, len(dep.Owners))

	for _, holder := range dep.Owners {
		if holder != owner {
			others = append(others, holder)
		}
	}

	if len(others) == 0 {
		return " (only here)"
	}

	return " (shared with " + strings.Join(others, ", ") + ")"
}

// originOf labels a row as the project's own package or a guest.
func originOf(entry Entry) string {
	if entry.Primary {
		return "primary"
	}

	return "guest"
}

// dash renders an empty field as "-", so a column never looks truncated.
func dash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// oneLine collapses a multi-line description onto one row; a newline inside a
// cell would break the column alignment.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
