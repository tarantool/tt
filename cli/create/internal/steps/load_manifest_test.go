package steps

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

const testManifestText = `
description: Cartridge template
vars:
    - prompt: Cluster cookie
      var: cluster_cookie
      default: cookie
      re: ^\w+$

    - prompt: User name
      var: user_name
      default: admin

pre-hook: ./hooks/pre-gen.sh
post-hook: ./hooks/post-gen.sh
`

func TestManifestLoad(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	if err = os.WriteFile(filepath.Join(workDir, "MANIFEST.yaml"),
		[]byte(testManifestText), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir

	loadManifest := LoadManifest{}
	if err := loadManifest.Run(createCtx, &templateCtx); err != nil {
		cmd := exec.Command("cat", filepath.Join(workDir, "MANIFEST.yaml"))
		stdout, _ := cmd.Output()
		fmt.Println(string(stdout))
		t.Errorf("Manifest load error: %s", err)
	}

	expectedManifest := templates.TemplateManifest{
		Description: "Cartridge template",
		Vars: []templates.UserPrompt{
			{
				Prompt:  "Cluster cookie",
				Var:     "cluster_cookie",
				Default: "cookie",
				Re:      `^\w+$`,
			},
			{
				Prompt:  "User name",
				Var:     "user_name",
				Default: "admin",
				Re:      "",
			},
		},
		PreHook:  "./hooks/pre-gen.sh",
		PostHook: "./hooks/post-gen.sh",
	}

	if templateCtx.IsManifestPresent == false {
		t.Errorf("Manifest present flag is set to false.")
	}

	if !reflect.DeepEqual(templateCtx.Manifest, expectedManifest) {
		t.Errorf("Manifest is loaded incorrectly: \n%s\n%s",
			templateCtx.Manifest, expectedManifest)
	}
}

func TestMissingManifest(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir

	loadManifest := LoadManifest{}
	if err := loadManifest.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Manifest load error: %s", err)
	}

	if templateCtx.IsManifestPresent == true {
		t.Errorf("Manifest present flag is set to true.")
	}
}

func TestManifestInvalidYaml(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	if err = os.WriteFile(filepath.Join(workDir, "MANIFEST.yaml"),
		[]byte(`Description: [`), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir

	loadManifest := LoadManifest{}
	if err := loadManifest.Run(createCtx, &templateCtx); err == nil {
		t.Errorf("Manifest load must fail.")
	}
}
