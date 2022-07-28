package steps

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

const testWorkDirName = "work-dir"

func TestCreateAppDirBasic(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temp dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	createCtx.AppName = "app1"
	createCtx.InstancesDir = workDir
	if err := createAppDir.Run(createCtx, &templateCtx); err != nil {
		t.Error("App dir creation failed.")
	}

	if templateCtx.AppPath != filepath.Join(workDir, createCtx.AppName) {
		t.Errorf("Invalid application path %s", templateCtx.AppPath)
	}
	checkForExistence(t, templateCtx.AppPath, 0755)

	// Check existing app handling.
	if err := createAppDir.Run(createCtx, &templateCtx); err == nil {
		t.Error("Existing app dir must cause error if force mode is disabled.")
	}

	// Create a file in app directory.
	tmpFileName := filepath.Join(workDir, "app1", "file")
	if err := os.WriteFile(tmpFileName, []byte(""), 0664); err != nil {
		t.Fatalf("Failed to write %s: %s", tmpFileName, err)
	}

	createCtx.ForceMode = true
	if err := createAppDir.Run(createCtx, &templateCtx); err != nil {
		t.Error("App dir creation failed with force mode enabled.")
	}

	// File is removed.
	if _, err := os.Stat(tmpFileName); err == nil {
		t.Errorf("File %s is not removed.", tmpFileName)
	}
}

func TestCreateAppDirMissingAppName(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temp dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	createCtx.InstancesDir = workDir
	if err := createAppDir.Run(createCtx, &templateCtx); err == nil {
		t.Error("App dir creation must fail if app and template names are not set.")
	}

	// Set template name.
	createCtx.TemplateName = "cartridge"
	if err := createAppDir.Run(createCtx, &templateCtx); err != nil {
		t.Error("App dir creation failed.")
	}

	if templateCtx.AppPath != filepath.Join(workDir, createCtx.TemplateName) {
		t.Errorf("Invalid application path %s", templateCtx.AppPath)
	}
	checkForExistence(t, templateCtx.AppPath, 0755)
}

func TestCreateAppDirMissingInstancesDir(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	createAppDir := CreateAppDirectory{}
	createCtx.AppName = "app1"
	createCtx.InstancesDir = testWorkDirName // Intances dir does not exist.
	defer os.RemoveAll(testWorkDirName)
	if err := createAppDir.Run(createCtx, &templateCtx); err == nil {
		t.Error("App dir creation must fail if instances directory does not exist.")
	}
}
