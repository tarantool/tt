package coredump

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
)

// vendorFile is where tt records where the files of a set of extensions it
// embeds come from: data of tt, no part of the schema of the manifest.
const vendorFile = "vendor.json"

// shortCommit is how many digits of a commit name it in the help and the
// log, as git abbreviates one.
const shortCommit = 8

// errVendorCommit reports a vendorFile that does not name one commit for
// all its files.
var errVendorCommit = errors.New("no one commit")

// vendorRecord is the content of vendorFile.
type vendorRecord struct {
	// Files are the copies, one entry each.
	Files []vendoredFile `json:"files"`
}

// vendoredFile is where a copy comes from.
type vendoredFile struct {
	// File is the name of the copy in the directory of the extensions.
	File string `json:"file"`
	// Repo is the GitHub repository of the file, owner/name.
	Repo string `json:"repo"`
	// Path is the path of the file in the repository.
	Path string `json:"path"`
	// Ref is the ref the file was taken at, a tag or a commit.
	Ref string `json:"ref"`
	// Commit is the full commit of the ref.
	Commit string `json:"commit"`
	// SHA256 is the checksum of the copy, in hex.
	SHA256 string `json:"sha256"`
	// Note says, if anything, what the ref is of.
	Note string `json:"note"`
}

// vendorCommit returns the commit the files of set were all taken at, as
// its vendorFile records it: the set is a copy of a repository at a
// commit, as the extensions of tarantool-gdb-tools are.
func (s extensionSet) vendorCommit() (string, error) {
	data, err := fs.ReadFile(s.files, s.dir+"/"+vendorFile)
	if err != nil {
		return "", fmt.Errorf("cannot read the record of the embedded GDB-extensions: %w", err)
	}

	var record vendorRecord

	err = json.Unmarshal(data, &record)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", s.name(vendorFile), err)
	}

	commit := ""

	for _, file := range record.Files {
		if (commit != "" && file.Commit != commit) || !fullCommitPattern.MatchString(file.Commit) {
			return "", fmt.Errorf("%s: %w: %s", s.name(vendorFile), errVendorCommit, file.File)
		}

		commit = file.Commit
	}

	if commit == "" {
		return "", fmt.Errorf("%s: %w: it lists no file", s.name(vendorFile), errVendorCommit)
	}

	return commit, nil
}

// GDBToolsCommit returns the commit of tarantool-gdb-tools the universal
// extensions tt embeds are of, abbreviated, as vendor.json of their copies
// records it: the help of inspect names it, so that pinning another commit
// changes the files and vendor.json alone. It is "unknown" when the record
// cannot be read, which a test of tt rules out.
func GDBToolsCommit() string {
	commit, err := gdbToolsSet.vendorCommit()
	if err != nil {
		return "unknown"
	}

	return commit[:shortCommit]
}
