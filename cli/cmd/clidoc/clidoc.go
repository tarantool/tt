// Package clidoc renders the command reference of tt as Markdown pages from
// the command tree tt runs: a page for every top-level command, with its
// subcommands under it, a page for every help topic, and an index of them.
//
// The text is the one --help prints - the description, the usage, the
// flags and the examples of every command - so that the reference says what
// tt says and changes with it. The pages are generated, not edited: the help
// of a command is where its text is written.
package clidoc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// IndexPage is the page of the root command, which lists the others.
	IndexPage = "index.md"

	pageDirMode  = 0o755
	pageFileMode = 0o644

	// header opens every page.
	header = "<!-- Generated from the help of tt by cli/cmd/clidoc; " +
		"edit the help of the command instead. -->\n\n"
)

var errNotRoot = errors.New("the command is not the root of its tree")

// Write renders the reference of the tree root into dir, creating it: the
// index, IndexPage, and a page named after every top-level command and help
// topic. It returns the names of the pages it wrote, the index first.
func Write(root *cobra.Command, dir string) ([]string, error) {
	if root.HasParent() {
		return nil, errNotRoot
	}

	err := os.MkdirAll(dir, pageDirMode)
	if err != nil {
		return nil, fmt.Errorf("cannot create the reference directory: %w", err)
	}

	pages := map[string]string{IndexPage: renderIndex(root)}
	names := []string{IndexPage}

	for _, cmd := range documented(root) {
		name := cmd.Name() + ".md"

		if cmd.IsAdditionalHelpTopicCommand() {
			pages[name] = renderTopic(cmd)
		} else {
			pages[name] = renderPage(cmd)
		}

		names = append(names, name)
	}

	for _, name := range names {
		err := os.WriteFile(filepath.Join(dir, name), []byte(pages[name]), pageFileMode)
		if err != nil {
			return nil, fmt.Errorf("cannot write the reference page: %w", err)
		}
	}

	return names, nil
}

// documented returns the subcommands of cmd the reference describes: those
// the help lists, which leaves out the hidden ones and cobra's own
// __complete.
func documented(cmd *cobra.Command) []*cobra.Command {
	var subs []*cobra.Command

	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			subs = append(subs, sub)
		}
	}

	return subs
}

// renderIndex renders the page of the root: its description, its usage and
// flags, and a list of the commands and the help topics.
func renderIndex(root *cobra.Command) string {
	var page strings.Builder

	page.WriteString(header)
	page.WriteString("# Commands\n\n")
	writeDescription(&page, root)
	writeUsage(&page, root)

	var commands, topics []*cobra.Command

	for _, cmd := range documented(root) {
		if cmd.IsAdditionalHelpTopicCommand() {
			topics = append(topics, cmd)
		} else {
			commands = append(commands, cmd)
		}
	}

	writeList(&page, "Commands", commands, func(cmd *cobra.Command) string {
		return cmd.Name() + ".md"
	})

	if len(topics) > 0 {
		page.WriteString("**Help topics**\n\n")

		for _, topic := range topics {
			fmt.Fprintf(&page, "- [`tt help %s`](%s.md) - %s\n", topic.Name(), topic.Name(),
				escape(topic.Short))
		}

		page.WriteString("\n")
	}

	writeFlags(&page, "Global flags", root.LocalNonPersistentFlags())
	page.WriteString("The global flags go before the command: `tt -V version`.\n")

	return page.String()
}

// renderPage renders the page of a top-level command: the command, then
// every command under it, depth first, each under a heading of its own.
func renderPage(cmd *cobra.Command) string {
	var page strings.Builder

	page.WriteString(header)
	page.WriteString("# " + cmd.CommandPath() + "\n\n")
	writeCommand(&page, cmd)

	var visit func(parent *cobra.Command)

	visit = func(parent *cobra.Command) {
		for _, sub := range documented(parent) {
			page.WriteString("## " + sub.CommandPath() + "\n\n")
			writeCommand(&page, sub)
			visit(sub)
		}
	}

	visit(cmd)

	return page.String()
}

// renderTopic renders the page of a help topic, which tt help prints.
func renderTopic(cmd *cobra.Command) string {
	var page strings.Builder

	page.WriteString(header)
	page.WriteString("# " + escape(cmd.Short) + "\n\n")
	page.WriteString("`tt help " + cmd.Name() + "` prints this text.\n\n")
	writeDescription(&page, cmd)

	return page.String()
}

// writeCommand writes what the help of cmd shows: its description, usage,
// aliases, subcommands, flags and examples.
func writeCommand(page *strings.Builder, cmd *cobra.Command) {
	writeDescription(page, cmd)
	writeUsage(page, cmd)

	if len(cmd.Aliases) > 0 {
		page.WriteString("Aliases: `" + strings.Join(cmd.Aliases, "`, `") + "`.\n\n")
	}

	writeList(page, "Commands", documented(cmd), func(sub *cobra.Command) string {
		return "#" + anchor(sub.CommandPath())
	})
	writeFlags(page, "Flags", cmd.LocalFlags())
	writeFlags(page, "Global flags", cmd.InheritedFlags())

	if lines := exampleLines(cmd.Example); len(lines) > 0 {
		page.WriteString("**Examples**\n\n")
		writeBlock(page, "console", dedent(lines))
	}
}

// exampleIndent is what the help template puts before the examples of a
// command. It indents their first line only: the lines after it carry their
// own indent.
const exampleIndent = "  "

// exampleLines returns the lines of the examples as the help prints them,
// without the blank lines around them.
func exampleLines(example string) []string {
	lines := strings.Split(exampleIndent+example, "\n")

	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}

	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// writeDescription writes the long description of cmd, or the short one
// when it has none.
func writeDescription(page *strings.Builder, cmd *cobra.Command) {
	text := cmd.Long
	if strings.TrimSpace(text) == "" {
		text = cmd.Short
	}

	page.WriteString(Markdown(text))
}

// writeUsage writes the usage lines of cmd, as its help shows them.
func writeUsage(page *strings.Builder, cmd *cobra.Command) {
	var lines []string

	if cmd.Runnable() {
		lines = append(lines, cmd.UseLine())
	}

	if cmd.HasAvailableSubCommands() {
		lines = append(lines, cmd.CommandPath()+" [flags] <command> [command flags]")
	}

	if len(lines) == 0 {
		return
	}

	page.WriteString("**Usage**\n\n")
	writeBlock(page, "text", lines)
}

// writeList writes a list of commands, each linked to target(cmd) and
// followed by its short description.
func writeList(page *strings.Builder, title string, cmds []*cobra.Command,
	target func(*cobra.Command) string,
) {
	if len(cmds) == 0 {
		return
	}

	page.WriteString("**" + title + "**\n\n")

	for _, cmd := range cmds {
		fmt.Fprintf(page, "- [`%s`](%s) - %s\n", cmd.CommandPath(), target(cmd), escape(cmd.Short))
	}

	page.WriteString("\n")
}

// writeFlags writes a list of the visible flags of set, the help flag
// excepted: every command has it.
func writeFlags(page *strings.Builder, title string, set *pflag.FlagSet) {
	var items []string

	set.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || flag.Name == "help" {
			return
		}

		items = append(items, flagItem(flag))
	})

	if len(items) == 0 {
		return
	}

	page.WriteString("**" + title + "**\n\n")

	for _, item := range items {
		page.WriteString(item + "\n")
	}

	page.WriteString("\n")
}

// flagItem renders flag as a list item: its names and the name of its value,
// then its usage and its default, as pflag prints them.
func flagItem(flag *pflag.Flag) string {
	name, usage := pflag.UnquoteUsage(flag)

	names := "`--" + flag.Name
	if name != "" {
		names += " " + name
	}

	names += "`"

	if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
		names = "`-" + flag.Shorthand + "`, " + names
	}

	item := "- " + names + " - " + escape(strings.Join(strings.Fields(usage), " "))

	if defaultShown(flag) {
		item += " (default `" + flag.DefValue + "`)"
	}

	return item
}

// defaultShown tells whether the help shows the default of flag: pflag
// leaves out the zero value of its type.
func defaultShown(flag *pflag.Flag) bool {
	switch flag.DefValue {
	case "", "false", "0", "[]", "<nil>", "0s":
		return false
	}

	return flag.NoOptDefVal == ""
}

// writeBlock writes lines as a fenced code block of lang.
func writeBlock(page *strings.Builder, lang string, lines []string) {
	page.WriteString("```" + lang + "\n")

	for _, line := range lines {
		page.WriteString(strings.TrimRight(line, " \t") + "\n")
	}

	page.WriteString("```\n\n")
}

// anchor returns the anchor of the heading text, as MkDocs makes it: lower
// case, spaces turned into dashes, other punctuation dropped.
func anchor(text string) string {
	var out strings.Builder

	for _, char := range strings.ToLower(text) {
		switch {
		case char == ' ' || char == '-':
			out.WriteRune('-')
		case char == '_' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9':
			out.WriteRune(char)
		}
	}

	return out.String()
}

// dedent removes the indentation lines have in common.
func dedent(lines []string) []string {
	common := -1

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))
		if common < 0 || indent < common {
			common = indent
		}
	}

	out := make([]string, len(lines))

	for i, line := range lines {
		if len(line) >= common && common > 0 {
			line = line[common:]
		}

		out[i] = line
	}

	return out
}
