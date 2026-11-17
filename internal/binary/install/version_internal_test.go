package install

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

// releaseTestSource is a source of tt that lists the tags of its releases
// and knows the latest one, as the releases of tt on GitHub do.
type releaseTestSource struct {
	*prebuiltTestSource

	releases    []string
	releasesErr error
	latest      string
	latestErr   error
	// listed counts the listings of the releases.
	listed int
}

func (s *releaseTestSource) Releases(context.Context) ([]string, error) {
	s.listed++

	return s.releases, s.releasesErr
}

func (s *releaseTestSource) Latest(context.Context) (string, error) {
	return s.latest, s.latestErr
}

// versionTestSources are a source of tarantool that lists releases of 3.7
// and 3.8 for the host, a pre-release of 3.9 and a debug build of it, and a
// source of tt with releases of tt 2 and a pre-release of tt 3.
func versionTestSources() (*prebuiltTestSource, *releaseTestSource) {
	tarantool := &prebuiltTestSource{product: binary.ProgramCe.String()}

	for _, release := range []string{"3.7.0", "3.7.1", "3.8.0", "3.9.0-rc1"} {
		tarantool.artifacts = append(tarantool.artifacts, bundle.Artifact{
			Version: release, Name: "tarantool-" + release + ".tar.gz", Locator: release,
		})
	}

	tarantool.artifacts = append(tarantool.artifacts, bundle.Artifact{
		Version: "3.9.0", Flavor: []string{"debug"}, Name: "debug", Locator: "debug",
	})

	tt := &releaseTestSource{
		prebuiltTestSource: &prebuiltTestSource{product: binary.ProgramTt.String()},
		releases: []string{
			"v3.0.0-alpha.2", "v3.0.0-alpha.1", "nightly", "v2.15.2", "v2.15.1", "v2.14.0",
		},
		latest: "v3.0.0-alpha.2",
	}

	return tarantool, tt
}

// fakeGitTags makes PATH a git alone, whose ls-remote lists tags. It
// returns the commands git has run.
func fakeGitTags(t *testing.T, tags ...string) func() []string {
	t.Helper()

	commands, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n" +
		"case \"$1\" in\n" +
		"ls-remote) printf 'abcdef0\\trefs/tags/%s\\n' " + strings.Join(tags, " ") + " ;;\n" +
		"*) exit 1 ;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(commands, "git"), []byte(script), 0o755))
	t.Setenv("PATH", commands)

	return func() []string {
		contents, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return nil
		}

		require.NoError(t, err)

		return strings.Split(strings.TrimSpace(string(contents)), "\n")
	}
}

// TestResolveInstallVersionWithoutGit resolves a release, a mask and the
// latest release of tarantool and of tt among the releases the source of
// prebuilt builds has, on a host without git, as version.MatchVersion
// resolves them among the Git tags: a mask is the newest release it
// matches, a pre-release only by its version.
func TestResolveInstallVersionWithoutGit(t *testing.T) {
	for _, test := range []struct {
		name      string
		program   binary.Program
		requested string
		want      string
	}{
		{name: "tarantool release", program: binary.ProgramCe, requested: "3.7.1", want: "3.7.1"},
		{name: "tarantool minor", program: binary.ProgramCe, requested: "3.7", want: "3.7.1"},
		{name: "tarantool major", program: binary.ProgramCe, requested: "3", want: "3.8.0"},
		{name: "tarantool latest", program: binary.ProgramCe, want: "3.8.0"},
		{
			name: "tarantool pre-release", program: binary.ProgramCe, requested: "3.9.0-rc1",
			want: "3.9.0-rc1",
		},
		{name: "tt release", program: binary.ProgramTt, requested: "2.15.1", want: "v2.15.1"},
		{name: "tt minor", program: binary.ProgramTt, requested: "2.15", want: "v2.15.2"},
		{name: "tt major", program: binary.ProgramTt, requested: "2", want: "v2.15.2"},
		{
			name: "tt pre-release", program: binary.ProgramTt, requested: "3.0.0-alpha.1",
			want: "v3.0.0-alpha.1",
		},
		{name: "tt latest", program: binary.ProgramTt, want: "v3.0.0-alpha.2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())

			tarantool, tt := versionTestSources()
			setTestBundleSources(t, tarantool, tt)

			resolved, err := resolveInstallVersion(InstallCtx{
				Program: test.program, requestedVersion: test.requested,
			})
			require.NoError(t, err)
			assert.Equal(t, tagInstallVersion(test.want), resolved)

			if test.program == binary.ProgramCe {
				// The source lists every build for the host.
				assert.Equal(t, []bundle.Query{binary.NewBundleQuery(test.program, false, "")},
					tarantool.queries)
			}
		})
	}
}

// TestResolveInstallVersionGitRequired names tt binary install, the lookup
// git is required for and what to do instead, on a host without git: a
// version the source of prebuilt builds does not resolve, a build from
// source, a commit, a pull request.
func TestResolveInstallVersionGitRequired(t *testing.T) {
	const (
		required   = "'git' is required for 'tt binary install' to "
		suggestion = "; install git, or ask for a version test-source has, such as "
		missing    = "prebuilt package unavailable: test-source has no prebuilt "
	)

	for _, test := range []struct {
		name string
		ctx  InstallCtx
		// change breaks the sources of tarantool and tt.
		change func(*prebuiltTestSource, *releaseTestSource)
		// noSources is a build of tt without sources of prebuilt builds.
		noSources bool
		want      string
		// listed is whether the releases of the source of the program are listed.
		listed bool
	}{
		{
			name: "development tag", listed: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.5.0-entrypoint"},
			want: required + "find tarantool 3.5.0-entrypoint: " + missing +
				"tarantool 3.5.0-entrypoint" + suggestion + "3.8.0",
		},
		{
			name: "tarantool release with no build", listed: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.9.1"},
			want: required + "find tarantool 3.9.1: " + missing + "tarantool 3.9.1" +
				suggestion + "3.8.0",
		},
		{
			name: "another series of tt", listed: true,
			ctx:  InstallCtx{Program: binary.ProgramTt, requestedVersion: "1.3"},
			want: required + "find tt 1.3: " + missing + "tt 1.3" + suggestion + "3.0.0-alpha.2",
		},
		{
			name: "no build for the host", listed: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.7"},
			change: func(tarantool *prebuiltTestSource, _ *releaseTestSource) {
				tarantool.artifacts = nil
			},
			want: required + "find tarantool 3.7: " + missing + "tarantool for " +
				runtime.GOOS + "/" + runtime.GOARCH + "; install git",
		},
		{
			name: "tarantool listing failure", listed: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.7"},
			change: func(tarantool *prebuiltTestSource, _ *releaseTestSource) {
				tarantool.listErr = errTestDelivery
			},
			want: required + "find tarantool 3.7: list tarantool archives in test-source: " +
				"connection reset; install git",
		},
		{
			name: "latest tarantool listing failure", listed: true,
			ctx: InstallCtx{Program: binary.ProgramCe},
			change: func(tarantool *prebuiltTestSource, _ *releaseTestSource) {
				tarantool.listErr = errTestDelivery
			},
			want: required + "find the latest tarantool: list tarantool archives in " +
				"test-source: connection reset; install git",
		},
		{
			name: "tt listing failure", listed: true,
			ctx: InstallCtx{Program: binary.ProgramTt, requestedVersion: "2.15"},
			change: func(_ *prebuiltTestSource, tt *releaseTestSource) {
				tt.releasesErr = errTestDelivery
			},
			want: required + "find tt 2.15: test-source: connection reset; install git",
		},
		{
			name: "latest tt failure", ctx: InstallCtx{Program: binary.ProgramTt},
			change: func(_ *prebuiltTestSource, tt *releaseTestSource) {
				tt.latestErr = errTestDelivery
			},
			want: required + "find the latest tt: no latest release of tt in test-source: " +
				"connection reset; install git",
		},
		{
			name: "no sources", noSources: true,
			ctx:  InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.7"},
			want: required + "find tarantool 3.7: install git",
		},
		{
			name: "source build",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "3.7.1", SourceBuild: true,
			},
			want: required + "find tarantool 3.7.1: install git",
		},
		{
			name: "dynamic",
			ctx:  InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.7.1", Dynamic: true},
			want: required + "find tarantool 3.7.1: install git",
		},
		{
			name: "docker",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "3.7.1", BuildInDocker: true,
			},
			want: required + "find tarantool 3.7.1: install git",
		},
		{
			name: "local repository",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "3.7.1", LocalRepo: "distfiles",
			},
			want: required + "find tarantool 3.7.1: install git",
		},
		{
			name: "tt source build", ctx: InstallCtx{Program: binary.ProgramTt, SourceBuild: true},
			want: required + "find the latest tt: install git",
		},
		{
			name: "commit",
			ctx:  InstallCtx{Program: binary.ProgramCe, requestedVersion: "abcdef0123"},
			want: required + "find commit abcdef0123 of tarantool: install git",
		},
		{
			name: "pull request",
			ctx:  InstallCtx{Program: binary.ProgramTt, requestedVersion: "pr/1234"},
			want: required + "find pull request 1234 of tt: install git",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())

			tarantool, tt := versionTestSources()
			if test.change != nil {
				test.change(tarantool, tt)
			}

			if test.noSources {
				setTestBundleSources(t)
			} else {
				setTestBundleSources(t, tarantool, tt)
			}

			_, err := resolveInstallVersion(test.ctx)
			require.ErrorIs(t, err, errGitRequired)
			require.EqualError(t, err, test.want)

			listed := len(tarantool.queries) > 0
			if test.ctx.Program == binary.ProgramTt {
				listed = tt.listed > 0
			}

			assert.Equal(t, test.listed, listed)
		})
	}
}

// TestResolveInstallVersionWithGit prefers the releases of the source of
// prebuilt builds to the Git tags where git is there too, and looks among
// the Git tags for what the source does not resolve.
func TestResolveInstallVersionWithGit(t *testing.T) {
	ceTags := []string{"3.5.0-entrypoint", "3.7.1", "3.8.0", "3.9.0", "3.10.0-entrypoint"}
	ttTags := []string{"v2.15.2", "v2.15.3", "v3.0.0-alpha.2"}

	for _, test := range []struct {
		name string
		ctx  InstallCtx
		// change breaks the sources of tarantool and tt.
		change func(*prebuiltTestSource, *releaseTestSource)
		want   string
		// git is whether git lists the tags.
		git bool
	}{
		{
			// The newest tag, 3.9.0, may have no prebuilt build yet.
			name: "the releases of the source", want: "3.8.0",
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3"},
		},
		{
			name: "the latest release of the source", want: "3.8.0",
			ctx: InstallCtx{Program: binary.ProgramCe},
		},
		{
			name: "development tag", want: "3.5.0-entrypoint", git: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.5.0-entrypoint"},
		},
		{
			name: "listing failure", want: "3.9.0", git: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.9"},
			change: func(tarantool *prebuiltTestSource, _ *releaseTestSource) {
				tarantool.listErr = errTestDelivery
			},
		},
		{
			name: "source build", want: "3.9.0", git: true,
			ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "3", SourceBuild: true},
		},
		{
			name: "tt release", want: "v2.15.2",
			ctx: InstallCtx{Program: binary.ProgramTt, requestedVersion: "2"},
		},
		{
			name: "rate limit of the tt releases", want: "v2.15.3", git: true,
			ctx: InstallCtx{Program: binary.ProgramTt, requestedVersion: "2"},
			change: func(_ *prebuiltTestSource, tt *releaseTestSource) {
				tt.releasesErr = errTestDelivery
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := ceTags
			if test.ctx.Program == binary.ProgramTt {
				tags = ttTags
			}

			calls := fakeGitTags(t, tags...)

			tarantool, tt := versionTestSources()
			if test.change != nil {
				test.change(tarantool, tt)
			}

			setTestBundleSources(t, tarantool, tt)

			resolved, err := resolveInstallVersion(test.ctx)
			require.NoError(t, err)
			assert.Equal(t, tagInstallVersion(test.want), resolved)

			var want []string

			if test.git {
				want = []string{"ls-remote --tags --refs " + test.ctx.Program.GitRepo()}
			}

			assert.Equal(t, want, calls())
		})
	}
}

func TestDefaultInstallTag(t *testing.T) {
	for _, test := range []struct {
		name    string
		program binary.Program
		tags    []string
		want    string
		wantErr error
	}{
		{
			name: "CE chooses the latest stable release", program: binary.ProgramCe,
			tags: []string{"3.8.0", "3.8.1", "4.0.0-rc1"}, want: "3.8.1",
		},
		{
			name:    "TT chooses the latest stable tt 3 and preserves tag spelling",
			program: binary.ProgramTt,
			tags:    []string{"v2.16.0", "v3.0.0", "v3.1.0", "v3.2.0-rc1", "v4.0.0"},
			want:    "v3.1.0",
		},
		{
			name: "TT takes a tt 3 prerelease while tt 3 has no release", program: binary.ProgramTt,
			tags: []string{"v2.16.0", "v3.0.0-rc1", "v3.0.0-rc2", "v4.0.0"}, want: "v3.0.0-rc2",
		},
		{
			name: "TT never takes tt 2", program: binary.ProgramTt,
			tags: []string{"v2.15.0", "v2.16.0"}, wantErr: errNoVersionsWereFetched,
		},
		{
			name: "CE needs a stable release", program: binary.ProgramCe,
			tags: []string{"4.0.0-rc1"}, wantErr: errNoVersionFound,
		},
		{name: "CE empty catalog", program: binary.ProgramCe, wantErr: errNoVersionFound},
		{name: "TT empty catalog", program: binary.ProgramTt, wantErr: errNoVersionsWereFetched},
		{
			name: "unsupported product", program: binary.ProgramTcm,
			wantErr: binary.ErrUnknownApplication,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var versions []version.Version

			for _, tag := range test.tags {
				parsed, err := version.Parse(tag)
				require.NoError(t, err)

				versions = append(versions, parsed)
			}

			actual, err := defaultInstallTag(test.program, versions)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, test.want, actual)
		})
	}
}

func TestResolveInstallVersionMasterWithoutRepository(t *testing.T) {
	t.Setenv("PATH", "")

	for _, program := range []binary.Program{binary.ProgramTt, binary.ProgramCe} {
		installCtx := InstallCtx{
			Program:          program,
			LocalRepo:        "missing",
			requestedVersion: masterVersion,
		}

		resolved, err := resolveInstallVersion(installCtx)
		require.NoError(t, err)
		assert.Equal(t, versionKindMaster, resolved.kind)
		assert.Equal(t, masterVersion, resolved.ref)
		assert.Equal(t, masterVersion, resolved.name)
	}
}

// TestInstallTarantoolBefore3 refuses a Tarantool older than 3.0 by name,
// before any lookup: it was taken for a commit hash and failed on its
// format.
func TestInstallTarantoolBefore3(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  InstallCtx
	}{
		{name: "release", ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "2.11.5"}},
		{name: "1.10", ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "1.10.15"}},
		{name: "mask", ctx: InstallCtx{Program: binary.ProgramCe, requestedVersion: "2.11"}},
		{
			name: "source build",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "2.11.5", SourceBuild: true,
			},
		},
		{
			name: "docker",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "2.11.5", BuildInDocker: true,
			},
		},
		{
			name: "dynamic",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "2.11.5", Dynamic: true,
			},
		},
		{
			name: "local repository",
			ctx: InstallCtx{
				Program: binary.ProgramCe, requestedVersion: "2.11.5", LocalRepo: "distfiles",
			},
		},
		{
			name: "enterprise",
			ctx:  InstallCtx{Program: binary.ProgramEe, requestedVersion: "2.11.1"},
		},
		{
			name: "enterprise archive",
			ctx: InstallCtx{
				Program: binary.ProgramEe, requestedVersion: "gc64-2.11.1-0-r609",
				LocalRepo: "bundles",
			},
		},
		{name: "sdk", ctx: InstallCtx{Program: binary.ProgramSDK, requestedVersion: "2.11"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			// Neither git nor a source is asked.
			t.Setenv("PATH", t.TempDir())
			setTestBundleSources(t)

			test.ctx.Scope = binary.ScopeProject

			err := Install(test.ctx)
			require.ErrorIs(t, err, binary.ErrUnsupportedVersion)
			require.ErrorContains(t, err, test.ctx.requestedVersion+
				": tt 3 installs Tarantool 3.0 and later only; use tt 2 to install Tarantool "+
				test.ctx.requestedVersion)
			assert.NoDirExists(t, ".tarantool")
		})
	}
}

// untrustedCertificateError is what a request to a TLS server whose
// certificate the system does not trust fails with, as on a system without
// its CA certificates.
func untrustedCertificateError(t *testing.T) error {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)

	client := download.Client{HTTP: &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12},
	}}}

	response, err := client.Get(t.Context(), server.URL)
	if err == nil {
		_ = response.Body.Close()
	}

	require.Error(t, err)

	return fmt.Errorf("fetch the index: %w", err)
}

// TestResolveInstallVersionUntrustedCertificate reports a source of
// prebuilt builds whose TLS certificate cannot be verified, with git there
// or not, rather than leave the version to the Git tags: git reaches its
// servers with the CA certificates of the same system.
func TestResolveInstallVersionUntrustedCertificate(t *testing.T) {
	const untrusted = "the TLS certificate of 127.0.0.1 cannot be verified: x509: " +
		"certificate signed by unknown authority; install the CA certificates of the system " +
		"(ca-certificates), or set SSL_CERT_FILE or SSL_CERT_DIR to a CA bundle"

	certificateErr := untrustedCertificateError(t)

	for _, test := range []struct {
		name string
		ctx  InstallCtx
		want string
	}{
		{
			name: "tarantool release",
			ctx:  InstallCtx{Program: binary.ProgramCe, requestedVersion: "3.9.0"},
			want: "'tt binary install' cannot list the prebuilt tarantool in test-source: " +
				untrusted,
		},
		{
			name: "latest tarantool", ctx: InstallCtx{Program: binary.ProgramCe},
			want: "'tt binary install' cannot list the prebuilt tarantool in test-source: " +
				untrusted,
		},
		{
			name: "tt mask", ctx: InstallCtx{Program: binary.ProgramTt, requestedVersion: "2.15"},
			want: "'tt binary install' cannot list the prebuilt tt in test-source: " + untrusted,
		},
		{
			name: "latest tt", ctx: InstallCtx{Program: binary.ProgramTt},
			want: "'tt binary install' cannot list the prebuilt tt in test-source: " + untrusted,
		},
	} {
		for _, git := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/git=%t", test.name, git), func(t *testing.T) {
				calls := func() []string { return nil }
				if git {
					calls = fakeGitTags(t, "3.9.0", "v2.15.2", "v3.0.0-alpha.2")
				} else {
					t.Setenv("PATH", t.TempDir())
				}

				tarantool, tt := versionTestSources()

				tarantool.listErr = certificateErr
				tt.releasesErr, tt.latestErr = certificateErr, certificateErr
				setTestBundleSources(t, tarantool, tt)

				_, err := resolveInstallVersion(test.ctx)
				require.EqualError(t, err, test.want)

				var certificate *download.CertificateError

				require.ErrorAs(t, err, &certificate)
				require.NotErrorIs(t, err, errGitRequired)
				assert.Empty(t, calls(), "git is not asked")
			})
		}
	}
}
