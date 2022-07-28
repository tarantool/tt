package steps

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

func TestRunHooks(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	preGenScript := filepath.Join(workDir, "pre-gen.sh")
	postGenScript := filepath.Join(workDir, "post-gen.sh")
	if err = os.WriteFile(preGenScript,
		[]byte(`#!/bin/sh
touch "$1/pre-script-invoked"
	`), 0775); err != nil {
		t.Fatalf("Failed to create file %s: %s", preGenScript, err)
	}
	if err = os.WriteFile(postGenScript,
		[]byte(`#!/bin/sh
touch "$1/post-script-invoked"
	`), 0775); err != nil {
		t.Fatalf("Failed to create file %s: %s", postGenScript, err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.PreHook = "pre-gen.sh"
	templateCtx.Manifest.PostHook = "post-gen.sh"

	runPreHook := RunHook{HookType: "pre"}
	runPostHook := RunHook{HookType: "post"}
	if err := runPreHook.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Pre-gen hook run failed: %s", err)
	}
	if err := runPostHook.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Post-gen hook run failed: %s", err)
	}

	preGenFileName := filepath.Join(templateCtx.AppPath, "pre-script-invoked")
	if _, err = os.Stat(preGenFileName); err != nil {
		t.Errorf("Error getting info of %s", preGenFileName)
	}

	postGenFileName := filepath.Join(templateCtx.AppPath, "post-script-invoked")
	if _, err = os.Stat(postGenFileName); err != nil {
		t.Errorf("Error getting info of %s", postGenFileName)
	}
}

func TestRunHooksMissingScript(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	preGenScript := filepath.Join(workDir, "pre-gen.sh")
	postGenScript := filepath.Join(workDir, "post-gen.sh")
	if err = os.WriteFile(preGenScript,
		[]byte(`#!/bin/sh
touch "$1/pre-script-invoked"
	`), 0775); err != nil {
		t.Fatalf("Failed to create file %s: %s", preGenScript, err)
	}
	if err = os.WriteFile(postGenScript,
		[]byte(`#!/bin/sh
touch "$1/post-script-invoked"
	`), 0775); err != nil {
		t.Fatalf("Failed to create file %s: %s", postGenScript, err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.PreHook = "pre-gen.sh"
	templateCtx.Manifest.PostHook = "post-gen.sh"

	runPreHook := RunHook{HookType: "pre"}
	runPostHook := RunHook{HookType: "post"}
	if err := runPreHook.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Pre-gen hook run failed: %s", err)
	}
	if err := runPostHook.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Post-gen hook run failed: %s", err)
	}

	preGenFileName := filepath.Join(templateCtx.AppPath, "pre-script-invoked")
	if _, err = os.Stat(preGenFileName); err != nil {
		t.Errorf("Error getting info of %s", preGenFileName)
	}

	postGenFileName := filepath.Join(templateCtx.AppPath, "post-script-invoked")
	if _, err = os.Stat(postGenFileName); err != nil {
		t.Errorf("Error getting info of %s", postGenFileName)
	}
}
