package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/instances"
)

// stageTemplates copies the project's .templates/ into the archive root, after
// checking that every template in it is complete and its instance description,
// if it has one, parses. A project without .templates/ stages nothing.
//
// The directory bypasses the component filter on purpose. Components select
// what lands under .rocks/, and their filter drops every hidden name ahead of
// the component's own include rules, so no manifest could bring .templates/
// back through it; nor should it - the templates describe how the installed
// version runs, not modules a loader finds. They go to the archive root next to
// the other names tt owns, and in both packing modes: --without-deps drops the
// runtime, not the description of the package.
//
// Checking at pack time is what keeps a broken template from reaching a host,
// where it would surface only at the first start of an instance, far from the
// author who could fix it.
func stageTemplates(stageDir, projectDir string) error {
	src := filepath.Join(projectDir, templatesDirName)

	// Lstat first, so a dangling .templates link is reported rather than
	// mistaken for a project without templates.
	_, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	info, err := os.Stat(src)
	if err != nil {
		return stateErrorf("%s: %w", templatesDirName, err)
	}

	if !info.IsDir() {
		return stateErrorf("%s: %w: not a directory", templatesDirName, errBadTemplate)
	}

	err = checkTemplates(src, projectDir)
	if err != nil {
		return err
	}

	dst := filepath.Join(stageDir, templatesDirName)

	return copyTreeFiltered(src, dst, "", isHiddenTemplateEntry)
}

// isHiddenTemplateEntry reports whether rel, a slash path relative to
// .templates/, is a hidden entry directly under it, such as the .gitkeep that
// keeps an empty .templates/ in git. Such an entry is not a template: it is
// neither checked nor packed. Hidden files inside a template are the
// template's own and travel with it like its other extra files.
func isHiddenTemplateEntry(rel string) bool {
	return rel != "" && !strings.Contains(rel, "/") && strings.HasPrefix(rel, ".")
}

// checkTemplates checks every entry of the .templates/ directory dir and
// reports all the broken ones together, so a project with several is fixed in
// one pass rather than one pack per template.
//
// Every entry but a hidden one has to be a template directory. A stray file
// directly under .templates/ is refused rather than packed along: nothing
// would read it, and a file where a template was meant (an instances.yaml one
// level too high, say) is better caught here than shipped. Hidden entries
// (.gitkeep) are skipped: they are how git keeps a directory, not templates.
func checkTemplates(dir, projectDir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return stateErrorf("reading %s: %w", templatesDirName, err)
	}

	var problems []error

	for _, entry := range entries {
		if isHiddenTemplateEntry(entry.Name()) {
			continue
		}

		problems = append(problems, checkTemplate(dir, entry.Name(), projectDir))
	}

	joined := errors.Join(problems...)
	if joined == nil {
		return nil
	}

	return stateErrorf("%w", joined)
}

// checkTemplate checks one entry of .templates/: it must be a directory holding
// the pair, and its instance description must parse.
//
// The description is parsed against the project directory, the directory a
// template's relative paths are resolved from, as they will be from the
// installed version. Parse reads nothing from it, so a path that exists only
// after the build or on the host does not fail the pack. The cluster config is
// checked only for presence: matching its topology against the description is
// the job of whoever loads the two together. It is needed by the Tarantool
// instances it configures alone, so a description of instances that run
// their scripts and of external processes makes a template without it, as it
// makes a pair when the template runs.
//
// A config without a description is a template too: it runs every instance
// of its topology, as a description of "*" alone would.
func checkTemplate(dir, name, projectDir string) error {
	label := templatesDirName + "/" + name
	path := filepath.Join(dir, name)

	// Stat, not Lstat: a template linked in from elsewhere is copied as its
	// target, like every other link pack meets.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", label, errBadTemplate, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s: %w: not a directory; every entry of %s/ is a template",
			label, errBadTemplate, templatesDirName)
	}

	present := map[string]bool{}

	for _, file := range []string{templateInstancesFile, templateConfigFile} {
		err = checkTemplateFile(filepath.Join(path, file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("%s: %w: %s: %w", label, errBadTemplate, file, err)
		}

		present[file] = true
	}

	switch {
	case present[templateInstancesFile]:
	case present[templateConfigFile]:
		return nil
	default:
		return fmt.Errorf("%s: %w: missing %s and %s; a template is %s, with %s next "+
			"to it unless it runs every instance of the topology, or %s alone when it "+
			"describes only instances that run their scripts and external processes",
			label, errBadTemplate, templateConfigFile, templateInstancesFile,
			templateConfigFile, templateInstancesFile, templateInstancesFile)
	}

	data, err := os.ReadFile(filepath.Join(path, templateInstancesFile))
	if err != nil {
		return fmt.Errorf("%s: %w: %w", label, errBadTemplate, err)
	}

	desc, err := instances.Parse(data, projectDir)
	if err != nil {
		return templateParseError(label, err)
	}

	if present[templateConfigFile] {
		return nil
	}

	configured := clusterRecords(desc)
	if len(configured) > 0 {
		return fmt.Errorf("%s: %w: missing %s to configure its Tarantool instances %s",
			label, errBadTemplate, templateConfigFile, strings.Join(configured, ", "))
	}

	return nil
}

// clusterRecords returns the quoted names of the records of desc that need
// the cluster config, in the order of the description: the Tarantool
// instances it configures, and "*", which stands for its topology.
func clusterRecords(desc instances.Description) []string {
	var names []string

	for index, record := range desc.Instances {
		if desc.Wildcard != nil && desc.Wildcard.At == index {
			names = append(names, fmt.Sprintf("%q", instances.WildcardName))
		}

		if record.Kind() == instances.KindCluster {
			names = append(names, fmt.Sprintf("%q", record.Name))
		}
	}

	if desc.Wildcard != nil && desc.Wildcard.At == len(desc.Instances) {
		names = append(names, fmt.Sprintf("%q", instances.WildcardName))
	}

	return names
}

// checkTemplateFile reports whether path is a regular file, following links.
// A missing file comes back as fs.ErrNotExist for the caller to collect.
func checkTemplateFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err //nolint:wrapcheck // The caller names the template and the file.
	}

	if !info.Mode().IsRegular() {
		return errNotRegular
	}

	return nil
}

// templateParseError names the template on every problem Parse found. Parse
// joins one error per problem, each rendered on its own line, so a single
// prefix would name the template on the first line only.
func templateParseError(label string, err error) error {
	prefix := label + "/" + templateInstancesFile

	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return fmt.Errorf("%s: %w: %w", prefix, errBadTemplate, err)
	}

	var problems []error

	for _, problem := range joined.Unwrap() {
		problems = append(problems, fmt.Errorf("%s: %w: %w", prefix, errBadTemplate, problem))
	}

	return errors.Join(problems...)
}
