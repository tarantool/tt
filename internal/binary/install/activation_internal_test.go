package install

import (
	"archive/tar"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func activationTestContext(t *testing.T, program binary.Program, delivery string) InstallCtx {
	t.Helper()

	ctx := InstallCtx{
		Program: program, Scope: binary.ScopeProject, Force: true, skipMasterUpdate: true,
	}

	switch delivery {
	case "prebuilt":
		if program == binary.ProgramTt {
			mockTtPrebuiltRepository(t)

			ctx.requestedVersion = "2.15.0"
		} else {
			mockPrebuiltRepository(t)

			ctx.requestedVersion = "3.7.1"
		}
	case "source":
		ctx.requestedVersion = masterVersion
		ctx.SourceBuild = true

		commands := t.TempDir()
		for command, script := range map[string]string{
			"git":   "#!/bin/sh\nexit 0\n",
			"mage":  "#!/bin/sh\nprintf 'new tt' > tt\n",
			"cmake": "#!/bin/sh\nexit 0\n",
			"make": `#!/bin/sh
set -e
mkdir -p tarantool-prefix/bin tarantool-prefix/include/tarantool
printf 'new Tarantool' > tarantool-prefix/bin/tarantool
printf 'headers' > tarantool-prefix/include/tarantool/module.h
`,
		} {
			require.NoError(t,
				os.WriteFile(filepath.Join(commands, command), []byte(script), 0o755))
		}

		t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	case "build", "build without headers":
		ctx.FromBuild = t.TempDir()

		executable := filepath.Join(ctx.FromBuild, "src", "tarantool")
		require.NoError(t, os.MkdirAll(filepath.Dir(executable), 0o755))
		require.NoError(t, os.WriteFile(executable, []byte("external Tarantool"), 0o755))

		if delivery == "build" {
			ctx.IncDir = t.TempDir()
		}
	case "bundle":
		ctx.LocalRepo = t.TempDir()
		ctx.requestedVersion = "3.7.1"

		if program == binary.ProgramSDK || program == binary.ProgramEe {
			writeTestSDK(t, ctx.LocalRepo, "gc64-3.7.1-0-r93", "")
		} else {
			name := "tcm-3.7.1.linux.amd64.tar.gz"

			if program == binary.ProgramTtEe {
				if runtime.GOOS != osLinux {
					t.Skip("tt Enterprise archives require Linux")
				}

				name = "tt-ee-3.7.1-linux-" + runtime.GOARCH + ".tar.gz"
			}

			archive := ttPackageTestArchive(t, ttArchiveEntry{
				name: program.Exec(), mode: 0o755, kind: tar.TypeReg, content: "new executable",
			})
			require.NoError(t, os.WriteFile(filepath.Join(ctx.LocalRepo, name), archive, 0o644))
		}
	default:
		t.Fatalf("unsupported test delivery: %s", delivery)
	}

	return ctx
}

func TestInstallationPreparationFailurePreservesActiveLinks(t *testing.T) {
	flows := []struct {
		program  binary.Program
		delivery string
	}{
		{binary.ProgramTt, "prebuilt"}, {binary.ProgramCe, "prebuilt"},
		{binary.ProgramTt, "source"}, {binary.ProgramCe, "source"},
		{binary.ProgramCe, "build"}, {binary.ProgramCe, "build without headers"},
		{binary.ProgramEe, "bundle"}, {binary.ProgramTtEe, "bundle"},
		{binary.ProgramTcm, "bundle"}, {binary.ProgramSDK, "bundle"},
	}

	for _, flow := range flows {
		for _, failure := range []string{"environment", "headers directory"} {
			t.Run(flow.program.String()+"/"+flow.delivery+"/"+failure, func(t *testing.T) {
				if failure == "headers directory" && !flow.program.IsTarantool() &&
					flow.program != binary.ProgramSDK {
					t.Skip("installation has no headers")
				}

				t.Chdir(t.TempDir())

				ctx := activationTestContext(t, flow.program, flow.delivery)
				root, err := ctx.Scope.ArtifactPath()
				require.NoError(t, err)

				paths := []string{"bin/tt", "bin/tarantool", "bin/tcm"}
				if failure == "environment" {
					paths = append(paths, "include/tarantool")

					require.NoError(t, os.WriteFile(".env", []byte("blocked"), 0o644))
				} else {
					require.NoError(t, os.MkdirAll(root, 0o755))
					require.NoError(t, os.WriteFile(filepath.Join(root, "include"), nil, 0o644))
				}

				for _, path := range paths {
					link := filepath.Join(root, path)
					require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
					require.NoError(t, os.Symlink("previous-"+filepath.Base(path), link))
				}

				// Repeated attempts must leave active selections untouched.
				for range 2 {
					err := Install(ctx)
					require.Error(t, err)

					if failure == "environment" {
						require.ErrorContains(t, err, "project environment directory")
					} else {
						require.ErrorContains(t, err, "not a directory")
					}

					for _, path := range paths {
						target, err := os.Readlink(filepath.Join(root, path))
						require.NoError(t, err)
						require.Equal(t, "previous-"+filepath.Base(path), target)
					}
				}

				blocked := ".env"
				if failure == "headers directory" {
					blocked = filepath.Join(root, "include")
				}

				require.NoError(t, os.Remove(blocked))
				require.NoError(t, Install(ctx))
				require.FileExists(t, ".env/activate.sh")
				require.FileExists(t, ".env/deactivate.sh")

				if flow.program == binary.ProgramSDK {
					assertSDKLinks(t, root, "gc64-3.7.1-0-r93")
				} else {
					active := filepath.Join(root, "bin", flow.program.Exec())
					require.FileExists(t, active)

					target, err := os.Readlink(active)
					require.NoError(t, err)
					require.NotEqual(t, "previous-"+flow.program.Exec(), target)
				}
			})
		}
	}
}
