package steps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/create/internal/steps"
)

// manifestNamed returns a project manifest whose package is called name.
func manifestNamed(name string) string {
	return "manifest_version = '0.1'\n\n[package]\nname = '" + name + "'\n"
}

func TestCheckPackageName(t *testing.T) {
	const form = "use lowercase letters, digits and dashes, starting with a letter, " +
		"and not a reserved name"

	tests := []struct {
		name     string
		appName  string
		manifest string
		wantErr  string
	}{
		{
			name:    "no manifest",
			appName: "My_App",
		},
		{
			name:     "package name",
			appName:  "my_app",
			manifest: manifestNamed("my-app"),
		},
		{
			name:     "capital letters",
			appName:  "My_App",
			manifest: manifestNamed("My-App"),
			wantErr: `application name "My_App" gives package.name "My-App" in ` +
				`app.manifest.toml, which is not a package name: ` + form,
		},
		{
			name:     "reserved name",
			appName:  "bin",
			manifest: manifestNamed("bin"),
			wantErr: `application name "bin" gives package.name "bin" in ` +
				`app.manifest.toml, which is not a package name: ` + form,
		},
		{
			name:     "no name",
			appName:  "app1",
			manifest: "manifest_version = '0.1'\n",
			wantErr: `application name "app1" gives package.name "" in ` +
				`app.manifest.toml, which is not a package name: ` + form,
		},
		{
			name:     "broken manifest",
			appName:  "app1",
			manifest: "manifest_version = '0.1'\n[package\n",
			wantErr:  "cannot read app.manifest.toml the template writes: ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appPath := t.TempDir()
			if test.manifest != "" {
				require.NoError(t, os.WriteFile(filepath.Join(appPath, "app.manifest.toml"),
					[]byte(test.manifest), 0o644))
			}

			createCtx := create_ctx.CreateCtx{AppName: test.appName}
			templateCtx := app_template.NewTemplateContext()

			templateCtx.AppPath = appPath

			err := steps.CheckPackageName{}.Run(&createCtx, &templateCtx)
			if test.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
