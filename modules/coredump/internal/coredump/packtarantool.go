package coredump

import (
	"debug/elf"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/scope"
)

// Candidate is a tarantool that may have dumped a core: pack checks a core
// it is given no tarantool for against each it finds, and packs it with
// the first that dumped it.
type Candidate struct {
	// Path is the path of the executable; empty with Err.
	Path string
	// Origin says what the tarantool is, as pack names it in what it logs
	// and in its errors: "the tarantool of PATH".
	Origin string
	// Err is why the tarantool could not be found; nil when it was.
	Err error
}

const (
	// namedOrigin is the Origin of the executable a core names.
	namedOrigin = "the executable the core names"
	// pathOrigin is the Origin of the tarantool of PATH.
	pathOrigin = "the tarantool of PATH"
	// tarantoolName is the name of a Tarantool executable, in PATH and in
	// the scopes tt binary installs into.
	tarantoolName = "tarantool"
	// scopeBinDir is the directory of a scope root with the links to the
	// active executables, as tt binary install and switch select them.
	scopeBinDir = "bin"
)

// installedProducts are the directories of versions/ of a scope root that
// tt binary installs the versions of Tarantool into: of Community and of
// Enterprise, each version in a directory of its own with the executable.
var installedProducts = []string{"tarantool", "tarantool-ee"}

// tarantoolChoice is the tarantool pack found for a core it was given none
// for, and how: what told it dumped the core, and the candidates tried
// before it, with why each was not taken.
type tarantoolChoice struct {
	// taken is the tarantool.
	taken Candidate
	// how says what told that taken dumped the core (see matchedBy); empty
	// when the core holds nothing to tell it by.
	how string
	// refused are the candidates tried before taken, in their order.
	refused []refusal
}

// refusal is a candidate pack tried for a core and did not take, and why.
type refusal struct {
	candidate Candidate
	// reason is why the candidate is not the tarantool that dumped the core.
	reason string
	// other reports a Tarantool executable of another build than the one
	// that dumped the core.
	other bool
}

// String returns the refusal as pack lists it: the path, what the
// candidate is, and why it was not taken.
func (r refusal) String() string {
	if r.candidate.Path == "" {
		return r.candidate.Origin + ": " + r.reason
	}

	return r.candidate.Path + ", " + r.candidate.Origin + ": " + r.reason
}

// isNamed reports whether candidate is the executable the core names.
func isNamed(candidate Candidate) bool {
	return candidate.Origin == namedOrigin
}

// tarantoolSearch is findTarantool at work: the core, the candidates tried,
// and the choice it makes.
type tarantoolSearch struct {
	core *coreDump
	// tried are the paths of the candidates tried, and seen the files found
	// at them.
	tried map[string]bool
	seen  []os.FileInfo
	// choice is what is found: the refusals of the candidates tried, and
	// the one taken.
	choice tarantoolChoice
}

// findTarantool finds the tarantool that dumped core, the core of opts, for
// opts of no Executable: the first of the candidates, in their order, that
// is an ELF executable of Tarantool, as hasSignature tells it, and dumped
// the core, as coreMismatch tells it, by its build ID; it is returned open.
// The candidates are the executable the core names (see namedTarantool),
// the tarantool of PATH, and those of opts.Tarantools, which is called
// only when neither of the first dumped the core. The core is read once
// for all of them, and a file tried before by another path, as a link to
// it, is passed over. A core that holds no image of its executable tells
// nothing to check them by, and the first Tarantool executable is taken.
//
// None taken is an error that lists each candidate tried, with why it was
// refused, and says to give the tarantool with -e, with the path the core
// records of it: errCoreMismatch when one was a Tarantool executable of
// another build, and errNoTarantool otherwise.
func findTarantool(core *coreDump, opts PackOptions) (*elf.File, *tarantoolChoice, error) {
	recorded := recordedExecutable(core)

	stages := []func() []Candidate{
		func() []Candidate { return namedTarantool(recorded) },
		pathTarantool,
	}

	if opts.Tarantools != nil {
		stages = append(stages, opts.Tarantools)
	}

	search := &tarantoolSearch{
		core: core, tried: map[string]bool{}, seen: nil,
		choice: tarantoolChoice{
			taken: Candidate{Path: "", Origin: "", Err: nil}, how: "", refused: nil,
		},
	}

	for _, stage := range stages {
		for _, candidate := range stage() {
			binary := search.try(candidate)
			if binary != nil {
				return binary, &search.choice, nil
			}
		}
	}

	return nil, nil, search.choice.notFound(opts.Core, recorded)
}

// try checks candidate, and takes it when it dumped the core: it returns
// its file, open, then, and nil otherwise, with the refusal recorded. A
// candidate tried before, by its path or its file, is passed over.
func (s *tarantoolSearch) try(candidate Candidate) *elf.File {
	if candidate.Err != nil {
		s.refuse(candidate, candidate.Err.Error(), false)

		return nil
	}

	if s.tried[candidate.Path] {
		return nil
	}

	s.tried[candidate.Path] = true

	info, err := os.Stat(candidate.Path)
	if err != nil {
		s.refuse(candidate, fileError(err), false)

		return nil
	}

	if slices.ContainsFunc(s.seen, func(seen os.FileInfo) bool { return os.SameFile(info, seen) }) {
		return nil
	}

	s.seen = append(s.seen, info)

	binary, err := elf.Open(candidate.Path)
	if err != nil {
		reason := "not an ELF file"

		_, unreadable := errors.AsType[*fs.PathError](err)
		if unreadable {
			reason = fileError(err)
		}

		s.refuse(candidate, reason, false)

		return nil
	}

	var (
		reason string
		other  bool
	)

	switch {
	case binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN:
		reason = fmt.Sprintf("an ELF file of type %s, not an executable", binary.Type)
	case !hasSignature(binary):
		reason = errNotTarantool.Error()
	default:
		reason, other = coreMismatch(s.core, binary), true
	}

	if reason != "" {
		_ = binary.Close()

		s.refuse(candidate, reason, other)

		return nil
	}

	s.choice.taken, s.choice.how = candidate, matchedBy(s.core, binary)

	return binary
}

// refuse records the refusal of candidate for reason.
func (s *tarantoolSearch) refuse(candidate Candidate, reason string, other bool) {
	s.choice.refused = append(s.choice.refused,
		refusal{candidate: candidate, reason: reason, other: other})
}

// fileError says why a file could not be read, of err of reading it:
// "missing" of one that is not there, and what the system says otherwise,
// without the path, which the refusal names.
func fileError(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "missing"
	}

	pathErr, ok := errors.AsType[*fs.PathError](err)
	if ok {
		return pathErr.Err.Error()
	}

	return err.Error()
}

// notFound returns the error of a search that took no tarantool for the
// core at corePath, which records its executable at recorded (see
// recordedExecutable).
func (c *tarantoolChoice) notFound(corePath, recorded string) error {
	if len(c.refused) == 0 {
		return fmt.Errorf("%w: either make sure it is there or specify it explicitly with "+
			"the -e option", errNoTarantool)
	}

	sentinel := errNoTarantool
	if slices.ContainsFunc(c.refused, func(refused refusal) bool { return refused.other }) {
		sentinel = errCoreMismatch
	}

	lines := make([]string, 0, len(c.refused))
	for _, refused := range c.refused {
		lines = append(lines, refused.String())
	}

	hint := "specify the tarantool that dumped the core with -e <path>"
	if recorded != "" {
		hint += "; the core records it as " + recorded
	}

	return fmt.Errorf("%w: none of the tarantools tried dumped '%s':\n  %s\n%s", sentinel,
		corePath, strings.Join(lines, "\n  "), hint)
}

// recordedExecutable returns the path core records of the executable of its
// process: that of the file mapped where its program headers are, AT_PHDR,
// in the NT_FILE note, which ends with " (deleted)" of a file removed since
// the process mapped it; "" when the core records none.
func recordedExecutable(core *coreDump) string {
	mapping, ok := core.executable()
	if !ok {
		return ""
	}

	return mapping.path
}

// namedTarantool returns the executable the core names, at the path it
// records, recorded, as a candidate. A file at the path of one deleted
// since it was run may be another of the same build, as of a package
// reinstalled, and is tried as well. A path that is not absolute, of a
// core that records none, is no candidate.
func namedTarantool(recorded string) []Candidate {
	path := strings.TrimSuffix(recorded, deletedSuffix)
	if !filepath.IsAbs(path) {
		return nil
	}

	return []Candidate{{Path: path, Origin: namedOrigin, Err: nil}}
}

// pathTarantool returns the tarantool of PATH as a candidate, none when
// PATH has none. An empty PATH is that of the system (see lookPath).
func pathTarantool() []Candidate {
	path, err := lookPath(tarantoolName)
	if err != nil {
		return nil
	}

	return []Candidate{{Path: path, Origin: pathOrigin, Err: nil}}
}

// ScopeTarantools returns the tarantools tt binary installed under root, the
// root of the scope name ("project", "user" or "system"), as candidates:
// the active one, bin/tarantool, then each version of Tarantool Community
// and Enterprise of versions/, in the order of the names of their
// directories. Those not there are left out; a directory of versions that
// cannot be read is a candidate with the error.
func ScopeTarantools(name, root string) []Candidate {
	var candidates []Candidate

	active := filepath.Join(root, scopeBinDir, tarantoolName)
	if isRegularFile(active) {
		candidates = append(candidates, Candidate{
			Path: active, Origin: "the active tarantool of the " + name + " scope", Err: nil,
		})
	}

	origin := "a tarantool tt binary installed into the " + name + " scope"

	for _, product := range installedProducts {
		dir := filepath.Join(root, scope.VersionsDirName, product)

		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			candidates = append(candidates, Candidate{Path: "", Origin: origin, Err: err})

			continue
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name(), tarantoolName)
			if isRegularFile(path) {
				candidates = append(candidates, Candidate{Path: path, Origin: origin, Err: nil})
			}
		}
	}

	return candidates
}

// matchedBy says what tells that binary dumped core, of a binary
// coreMismatch finds the executable of core: its build ID, that of the
// image of the executable in the core, or, with no build ID on a side, its
// entry point and its program headers; "" when the core holds no image of
// its executable, and tells nothing.
func matchedBy(core *coreDump, binary *elf.File) string {
	image, err := programImage(core, binary)
	if err != nil {
		return ""
	}

	id := core.imageBuildID(image)
	if id != "" && buildID(binary) != "" {
		return "it has the build ID of the core, " + id
	}

	return "it has the entry point and the program headers of the executable of the core"
}

// logTarantoolChoice logs which tarantool pack found for a core it was given
// none for, as choice says, and why: the executable the core names, or
// another one, with what told it dumped the core, and then why the one the
// core names was not taken. The other candidates refused before it are
// debug lines. Another one than the executable the core names taken of a
// core that tells nothing to check it by is a warning, with the hint of
// -e. A nil choice, of a tarantool given, logs nothing.
func logTarantoolChoice(log *slog.Logger, choice *tarantoolChoice) {
	if choice == nil {
		return
	}

	for _, refused := range choice.refused {
		line := "not packing with " + refused.String()
		if isNamed(refused.candidate) {
			log.Info(line)
		} else {
			log.Debug(line)
		}
	}

	line := "packing with " + choice.taken.Path + ", " + choice.taken.Origin

	switch {
	case isNamed(choice.taken):
		log.Info(line)
	case choice.how != "":
		log.Info(line + ": " + choice.how)
	default:
		warnWithHint(log, line+", which may not be the tarantool that dumped the core: the "+
			"core holds no page of its executable to check it against",
			"pack the core with the tarantool that dumped it, -e <path>")
	}
}
