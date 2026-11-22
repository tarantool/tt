package rws

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realIndex is two paragraphs of dists/static/main/binary-amd64/Packages of
// the repository as it was on 2026-11-23, in its order of fields.
const realIndex = `Architecture: amd64
Description: Tarantool is a high performance in-memory database and Lua application server.
 Tarantool supports replication, online backup and stored procedures in Lua.
 The package provides Tarantool server binary.
Homepage: https://tarantool.org
Maintainer: Tarantool Team <admin@tarantool.org>
Package: tarantool
Priority: optional
Section: database
Version: 3.9.0-1
Installed-Size: 107774
Size: 38857536
MD5Sum: c0ef4e22cc2a7f3359e2539041272fe6
SHA1: 4e82a0f093688f0817876b2a169c2ce8014775cd
SHA256: 5e26c95ea0f2535fe0ac12ce7a1fa445467afaf92a5be549c1d807a2de90ba4f
Filename: pool/static/main/t/tarantool/tarantool_3.9.0-1_amd64.deb
FileTime: 1792748634.0

Architecture: amd64
Depends: tarantool (= 3.9.0-1)
Description: Tarantool is a high performance in-memory database and Lua application server.
 Tarantool supports replication, online backup and stored procedures in Lua.
 The package provides Tarantool server development files needed for pluggable
 modules.
Homepage: https://tarantool.org
Maintainer: Tarantool Team <admin@tarantool.org>
Package: tarantool-dev
Priority: optional
Section: libdevel
Version: 3.9.0-1
Installed-Size: 436
Size: 101706
MD5Sum: a317b1fc7905a6c2abb63ed81629b442
SHA1: d469e69af75b8164f4c0853b7e7758ef4fe88865
SHA256: b8c9985ff5fe94a3766b240559ece99d101d85c7bc4f760d932c2012a8c61f34
Filename: pool/static/main/t/tarantool/tarantool-dev_3.9.0-1_amd64.deb
FileTime: 1792748634.0
`

func TestParseIndex(t *testing.T) {
	paragraphs, err := parseIndex(strings.NewReader("\n" + realIndex + "\n \n"))
	require.NoError(t, err)
	require.Len(t, paragraphs, 2)
	assert.Equal(t, "tarantool", paragraphs[0]["package"])
	assert.Equal(t, "Tarantool is a high performance in-memory database and Lua application "+
		"server.\nTarantool supports replication, online backup and stored procedures in Lua.\n"+
		"The package provides Tarantool server binary.", paragraphs[0]["description"])
	assert.Equal(t, "pool/static/main/t/tarantool/tarantool_3.9.0-1_amd64.deb",
		paragraphs[0]["filename"])
	assert.Equal(t, "tarantool-dev", paragraphs[1]["package"])
	assert.Equal(t, "tarantool (= 3.9.0-1)", paragraphs[1]["depends"])

	artifacts := selectPackages(paragraphs, "https://packages.test/", "3.9.0", "amd64")
	require.Len(t, artifacts, 1)
	assert.Equal(t, "3.9.0", artifacts[0].Version)
	assert.Equal(t, "tarantool_3.9.0-1_amd64.tar.gz", artifacts[0].Name)
	assert.Equal(t, locator{
		Runtime: packageFile{
			URL: "https://packages.test/pool/static/main/t/tarantool/" +
				"tarantool_3.9.0-1_amd64.deb",
			Size:   38857536,
			SHA256: "5e26c95ea0f2535fe0ac12ce7a1fa445467afaf92a5be549c1d807a2de90ba4f",
		},
		Headers: packageFile{
			URL: "https://packages.test/pool/static/main/t/tarantool/" +
				"tarantool-dev_3.9.0-1_amd64.deb",
			Size:   101706,
			SHA256: "b8c9985ff5fe94a3766b240559ece99d101d85c7bc4f760d932c2012a8c61f34",
		},
	}, packageTestLocator(t, artifacts[0]))
}

func TestParseIndexErrors(t *testing.T) {
	for _, test := range []struct {
		name, index, message string
	}{
		{"HTML", "<html>\n<body>Not Found</body>\n</html>\n", "line 1 is no field"},
		{"continuation first", " Tarantool\n", "line 1 continues no field"},
		{"continuation after a paragraph", "Package: tarantool\n\n more\n",
			"line 3 continues no field"},
		{"name with a space", "Package: tarantool\nSHA 256: x\n", "line 2 is no field"},
		{"empty name", ": tarantool\n", "line 1 is no field"},
		{"repeated field", "Package: tarantool\nVersion: 3.9.0-1\npackage: tarantool-dev\n",
			"line 3 repeats the field package"},
		{"line too long", "Description: " + strings.Repeat("x", maxIndexLine) + "\n",
			"token too long"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseIndex(strings.NewReader(test.index))
			require.ErrorIs(t, err, errInvalidIndex)
			require.ErrorContains(t, err, test.message)
		})
	}
}

// testDigest is the SHA-256 of name, a digest the index of a test lists.
func testDigest(name string) string {
	digest := sha256.Sum256([]byte(name))

	return hex.EncodeToString(digest[:])
}

// testParagraph is the paragraph of the index for the package name of
// version and arch, with the file name the repository gives it, which has
// no epoch.
func testParagraph(name, version, arch string) paragraph {
	_, fileVersion, found := strings.Cut(version, ":")
	if !found {
		fileVersion = version
	}

	filename := "pool/static/main/t/tarantool/" + name + "_" + fileVersion + "_" + arch + ".deb"

	return paragraph{
		"package": name, "version": version, "architecture": arch, "filename": filename,
		"size": strconv.Itoa(len(filename)), "sha256": testDigest(filename),
	}
}

// testBuild is the pair of paragraphs of a build.
func testBuild(version, arch string) []paragraph {
	return []paragraph{
		testParagraph(runtimePackage, version, arch), testParagraph(headersPackage, version, arch),
	}
}

// with returns fields with the field name set to value, or without it for
// an empty value.
func with(fields paragraph, name, value string) paragraph {
	changed := maps.Clone(fields)

	if value == "" {
		delete(changed, name)
	} else {
		changed[name] = value
	}

	return changed
}

func TestSelectPackages(t *testing.T) {
	const repositoryURL = packageTestURL + "/"

	valid := testParagraph(runtimePackage, "3.7.1-1", "amd64")

	tests := []struct {
		name       string
		paragraphs [][]paragraph
		line       string
		// want are the versions of the files of each build, which have no
		// epoch, by its version.
		want     map[string]string
		messages []string
	}{
		{
			name: "exact tag and newest numeric revision",
			paragraphs: [][]paragraph{
				testBuild("3.7.1-2", "amd64"), testBuild("3.7.1-10", "amd64"),
				{testParagraph(runtimePackage, "3.7.10-99", "amd64")},
				testBuild("3.7.1-99", "arm64"),
				{testParagraph("tarantool-dbg", "3.7.1-99", "amd64")},
				testBuild("3.7.1-beta-99", "amd64"),
			},
			line: "3.7.1",
			want: map[string]string{"3.7.1": "3.7.1-10"},
		},
		{
			name: "epoch and tilde",
			paragraphs: [][]paragraph{
				testBuild("3.7.1-1", "amd64"), testBuild("3.7.1-2~rc1", "amd64"),
				testBuild("1:3.7.1-0", "amd64"), testBuild("3.7.1-1+b1", "amd64"),
			},
			line: "3.7.1",
			// The epoch wins over the higher revisions.
			want: map[string]string{"3.7.1": "3.7.1-0"},
		},
		{
			name: "tilde before the release",
			paragraphs: [][]paragraph{
				testBuild("3.7.1-1~rc1", "amd64"), testBuild("3.7.1-1", "amd64"),
			},
			want: map[string]string{"3.7.1": "3.7.1-1"},
		},
		{
			name:       "missing runtime",
			paragraphs: [][]paragraph{{testParagraph(headersPackage, "3.7.1-1", "amd64")}},
			line:       "3.7.1",
		},
		{
			name: "missing matching newest headers does not downgrade",
			paragraphs: [][]paragraph{
				testBuild("3.7.1-1", "amd64"), {testParagraph(runtimePackage, "3.7.1-2", "amd64")},
			},
			line:     "3.7.1",
			messages: []string{"no matching headers package tarantool-dev 3.7.1-2 for tarantool"},
		},
		{
			name: "headers of another architecture",
			paragraphs: [][]paragraph{{
				testParagraph(runtimePackage, "3.7.1-1", "amd64"),
				testParagraph(headersPackage, "3.7.1-1", "arm64"),
			}},
		},
		{
			name: "release line",
			paragraphs: [][]paragraph{
				testBuild("3.7.0-1", "amd64"), testBuild("3.7.1-1", "amd64"),
				testBuild("3.8.0-1", "amd64"), testBuild("3.70.0-1", "amd64"),
			},
			line: "3.7",
			want: map[string]string{"3.7.0": "3.7.0-1", "3.7.1": "3.7.1-1"},
		},
		{
			name: "every version",
			paragraphs: [][]paragraph{
				testBuild("3.7.0-1", "amd64"), testBuild("3.8.0-1", "amd64"),
				testBuild("3.0.0", "amd64"),
			},
			want: map[string]string{"3.0.0": "3.0.0", "3.7.0": "3.7.0-1", "3.8.0": "3.8.0-1"},
		},
		{
			name: "digest in upper case",
			paragraphs: [][]paragraph{{
				with(valid, "sha256", strings.ToUpper(valid["sha256"])),
				testParagraph(headersPackage, "3.7.1-1", "amd64"),
			}},
			want: map[string]string{"3.7.1": "3.7.1-1"},
		},
		{
			name: "invalid packages",
			paragraphs: [][]paragraph{
				testBuild("3.7.1-1", "amd64"),
				{
					with(valid, "version", "3.8.0-1"),
					with(testParagraph(headersPackage, "3.8.0-1", "amd64"), "sha256", "x"),
				},
				{with(valid, "version", "2.11.8-1"), with(valid, "version", ":3.7.2-1")},
				{with(valid, "version", "3.7.3-1!"), with(valid, "version", "")},
				{with(valid, "filename", "../tarantool_3.7.4-1_amd64.deb")},
				{with(valid, "filename", "/pool/tarantool_3.7.4-1_amd64.deb")},
				{with(valid, "filename", "pool/../../tarantool_3.7.4-1_amd64.deb")},
				{with(valid, "filename", "pool/tarantool-3.7.4-1.x86_64.rpm")},
				{with(valid, "filename", "pool/tarantool_3.7.4-1_amd64.deb?x")},
				{with(valid, "filename", "")},
				{with(valid, "size", "0"), with(valid, "size", "-1"), with(valid, "size", "")},
			},
			want: map[string]string{"3.7.1": "3.7.1-1"},
			messages: []string{
				"skip tarantool-dev 3.8.0-1 in the package index: invalid package: SHA-256",
				"skip tarantool 2.11.8-1 in the package index: invalid package: " +
					"no version of Tarantool 3",
				`invalid package: file name "../tarantool_3.7.4-1_amd64.deb"`,
				`invalid package: size "0"`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := recordLogs(t)

			var paragraphs []paragraph

			for _, group := range test.paragraphs {
				paragraphs = append(paragraphs, group...)
			}

			got := make(map[string]string)

			for _, artifact := range selectPackages(paragraphs, repositoryURL, test.line, "amd64") {
				packages := packageTestLocator(t, artifact)

				assert.Empty(t, artifact.Flavor)
				assert.Empty(t, artifact.SHA256, "the digests are those of the packages")
				require.True(t, packages.Runtime.valid())
				require.True(t, packages.Headers.valid())

				runtimeName := strings.TrimPrefix(packages.Runtime.URL,
					repositoryURL+"pool/static/main/t/tarantool/")
				version := strings.TrimSuffix(strings.TrimPrefix(runtimeName, "tarantool_"),
					"_amd64.deb")
				assert.Equal(t, "tarantool_"+version+"_amd64.tar.gz", artifact.Name)
				assert.Equal(t, testDigest(strings.TrimPrefix(packages.Runtime.URL, repositoryURL)),
					packages.Runtime.SHA256)
				assert.Equal(t, repositoryURL+"pool/static/main/t/tarantool/tarantool-dev_"+
					version+"_amd64.deb", packages.Headers.URL, "the headers of the same version")

				got[artifact.Version] = version
			}

			if test.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, test.want, got)
			}

			for _, message := range test.messages {
				requireLogged(t, recorder, message)
			}
		})
	}
}

func TestCompareDebVersions(t *testing.T) {
	parse := func(raw string) debVersion {
		t.Helper()

		version, ok := parseDebVersion(raw)
		require.True(t, ok, raw)

		return version
	}

	// The pairs dpkg --compare-versions orders so.
	for _, pair := range [][2]string{
		{"3.9.0-1", "3.9.0-2"}, {"3.9.0-2", "3.9.0-10"}, {"3.9.0-1~rc1", "3.9.0-1"},
		{"3.9.0-1", "3.9.0-1a"}, {"3.9.0-1a", "3.9.0-1+b1"}, {"3.9.0-1.9", "3.9.0-1.10"},
		{"3.9.0-1", "3.9.0-1.1"}, {"3.9.0-1~~", "3.9.0-1~"}, {"3.9.0-9", "1:3.9.0-0"},
		{"3.9.0-1", "3.10.0-1"}, {"3.9.0-1ubuntu1", "3.9.0-1ubuntu2"},
	} {
		left, right := parse(pair[0]), parse(pair[1])
		require.Negative(t, compareDebVersions(left, right), "%s < %s", pair[0], pair[1])
		require.Positive(t, compareDebVersions(right, left), "%s > %s", pair[1], pair[0])
	}

	for _, pair := range [][2]string{
		{"3.9.0-1", "3.9.0-01"}, {"0:3.9.0-1", "3.9.0-1"}, {"3.9.0", "3.9.0-0"},
	} {
		require.Zero(t, compareDebVersions(parse(pair[0]), parse(pair[1])), pair)
	}

	assert.Equal(t, debVersion{raw: "1:3.9.0-1+b1", epoch: 1, upstream: "3.9.0", revision: "1+b1"},
		parse("1:3.9.0-1+b1"))
	assert.Equal(t, debVersion{raw: "3.7.1-beta-99", epoch: 0, upstream: "3.7.1-beta",
		revision: "99"}, parse("3.7.1-beta-99"))

	// A pre-release is no Git tag of Tarantool: 3.9.0~rc1 is not listed.
	for _, raw := range []string{
		"", "2.11.8-1", "3.9-1", "3.9.0-", "3.9.0-1_1", "x:3.9.0-1", ":3.9.0-1",
		"99999999999:3.9.0-1", "3.9.0 -1", "master", "3.9.0~rc1-1",
	} {
		_, ok := parseDebVersion(raw)
		assert.False(t, ok, raw)
	}
}

func FuzzParseIndex(f *testing.F) {
	f.Add(realIndex)
	f.Add("Package: tarantool\n continued\n\n\nVersion: 3.9.0-1\n")
	f.Fuzz(func(t *testing.T, index string) {
		paragraphs, err := parseIndex(strings.NewReader(index))
		if err != nil {
			return
		}

		_ = selectPackages(paragraphs, packageTestURL, "", "amd64")
	})
}
