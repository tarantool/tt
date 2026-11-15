package cc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/tarantool/tt/sdk/build"
)

// This file makes the objects of a build independent of where the project
// is: the paths of the debug information relative to the project, with the
// flags the compiler of each language takes, probed once a compiler.

// noRecordSwitches keeps the command line of a compile, with its paths,
// out of the debug information, where gcc of some distributions records it
// (DW_AT_producer).
const noRecordSwitches = "-gno-record-gcc-switches"

// reproducibleFlags are the flags that keep the paths of the machine out of
// an object compiled in a project of the directory project: the project
// itself is ".", in the debug information, __FILE__ and the like
// (-ffile-prefix-map, of gcc 8 and clang 10), and the command line is not
// recorded. The work directory, where the objects are, is not mapped: the
// objects do not name it, only -o and -MF do, which the compiler does not
// record.
func reproducibleFlags(project string) []string {
	if project == "" {
		return []string{noRecordSwitches}
	}

	return []string{"-ffile-prefix-map=" + project + "=.", noRecordSwitches}
}

// probeFile is the record of the flags probed, in the work directory.
const probeFile = "flags.json"

// probeRecord is the record of the flags probed: whether each is taken, by
// the key of the compiler, the language and the flag (probeKey).
type probeRecord struct {
	Supported map[string]bool `json:"supported"`
}

// probeKey is the key of the probe of flag by the compiler of identity for
// lang.
func probeKey(identity string, lang language, flag string) string {
	key := newKey("probe")

	key.add(identity, lang.String(), flag)

	return key.sum()
}

// prober probes the flags of the compilers of a build, the answers of the
// earlier builds kept in the work directory.
type prober struct {
	c      *build.Context
	record probeRecord
	// changed reports that a flag was probed, and the record is to be
	// written.
	changed bool
}

// newProber returns the prober of the build of c, with the record of its
// work directory: none, or one that cannot be read, probes every flag.
func newProber(c *build.Context) *prober {
	probe := &prober{c: c, record: probeRecord{Supported: map[string]bool{}}, changed: false}

	data, err := os.ReadFile(filepath.Join(c.Dirs.Work, probeFile))
	if err == nil {
		var record probeRecord

		if json.Unmarshal(data, &record) == nil && record.Supported != nil {
			probe.record = record
		}
	}

	return probe
}

// supported returns those of flags the compiler of words, whose identity is
// identity, takes for a source of lang, in their order.
func (p *prober) supported(ctx context.Context, words []string, identity string, lang language,
	flags []string,
) []string {
	var taken []string

	for _, flag := range flags {
		key := probeKey(identity, lang, flag)

		ok, known := p.record.Supported[key]
		if !known {
			ok = p.probe(ctx, words, lang, flag)
			p.record.Supported[key] = ok
			p.changed = true
		}

		if ok {
			taken = append(taken, flag)
		}
	}

	return taken
}

// probeSources are the sources a flag is probed with, by language.
var probeSources = map[language]string{
	langC:   "int main(void) { return 0; }\n",
	langCXX: "int main() { return 0; }\n",
}

// probe reports whether the compiler of words takes flag for a source of
// lang: it compiles a source of nothing with it, which must succeed and
// print nothing, as cc-rs probes a flag, since gcc warns of some flags it
// does not know rather than fail.
func (p *prober) probe(ctx context.Context, words []string, lang language, flag string) bool {
	dir, err := os.MkdirTemp(p.c.Dirs.Work, "probe-")
	if err != nil {
		return false
	}

	defer func() { _ = os.RemoveAll(dir) }()

	source := filepath.Join(dir, "flag_check.c")
	if lang == langCXX {
		source = filepath.Join(dir, "flag_check.cpp")
	}

	err = os.WriteFile(source, []byte(probeSources[lang]), filePerm)
	if err != nil {
		return false
	}

	argv := concat(words, []string{flag, "-c", source, "-o", filepath.Join(dir, "flag_check.o")})
	cmd := p.c.Command(ctx, argv[0], argv[1:]...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger(p.c).DebugContext(ctx, "probing a flag of the compiler", "command",
		commandLine(cmd.Args))

	err = cmd.Run()

	return err == nil && stderr.Len() == 0
}

// save writes the record of the flags probed into the work directory, when
// a flag was probed. A record that cannot be written probes again next
// time, which fails no build.
func (p *prober) save(ctx context.Context) {
	if !p.changed {
		return
	}

	err := writeRecord(filepath.Join(p.c.Dirs.Work, probeFile), p.record)
	if err != nil {
		logger(p.c).DebugContext(ctx, "the flags probed are not kept: "+err.Error())
	}
}

// probeReproducible sets the reproducible flags (reproducibleFlags) each
// compiler of built takes into tools, for the languages it compiles: those
// of CC for C and assembler, those of CXX for C++. identities are the
// identities of the compilers, by their command lines.
func probeReproducible(ctx context.Context, c *build.Context, tools *toolchain, built plan,
	identities map[string]string,
) {
	probe := newProber(c)
	flags := reproducibleFlags(c.Dirs.Project)

	tools.reproducible = map[language][]string{}

	for _, step := range built.steps {
		lang := langC
		if step.lang == langCXX {
			lang = langCXX
		}

		if _, done := tools.reproducible[lang]; done {
			continue
		}

		tools.reproducible[lang] = probe.supported(ctx, step.compiler,
			identities[commandLine(step.compiler)], lang, flags)
	}

	probe.save(ctx)
}

// reproducibleOf are the reproducible flags of tools for the sources of
// lang.
func (t toolchain) reproducibleOf(lang language) []string {
	if lang == langCXX {
		return slices.Clone(t.reproducible[langCXX])
	}

	return slices.Clone(t.reproducible[langC])
}
