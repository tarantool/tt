package smoke_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	smoke "example.com/tt-smoke"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/build/v3/buildtest"
	"github.com/tarantool/tt/sdk/v3/sdktest"
)

func TestGreet(t *testing.T) {
	got := sdktest.RunModule(t, smoke.New, "smoke", "greet", "world", "-o", "json")
	if got.Err != nil {
		t.Fatal(got.Err)
	}

	var result map[string]string

	err := json.Unmarshal([]byte(got.Stdout), &result)
	if err != nil {
		t.Fatalf("%q: %s", got.Stdout, err)
	}

	if result["greeting"] != "hello, world" {
		t.Errorf("greeting %q, want %q", result["greeting"], "hello, world")
	}
}

func TestGreetNobody(t *testing.T) {
	got := sdktest.RunModule(t, smoke.New, "smoke", "greet", " ")

	var usage *sdk.UsageError
	if !errors.As(got.Err, &usage) {
		t.Fatalf("error %v, want a usage error", got.Err)
	}
}

// component is the component the tests build.
var component = build.Component{
	Name: "greeting", Path: "greeting", Namespace: "demo", Package: "demo",
}

func TestBackend(t *testing.T) {
	bld, err := buildtest.Validate(smoke.Backend(), component,
		buildtest.Block{Fields: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatal(err)
	}

	harness := buildtest.New(t, smoke.Backend(), component)

	err = harness.Run(t.Context(), bld)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Share, "greeting.lua"))
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "return \"hello\"\n" {
		t.Errorf("greeting.lua %q", data)
	}
}

func TestBackendNoText(t *testing.T) {
	_, err := buildtest.Validate(smoke.Backend(), component, buildtest.Block{})
	if err == nil || err.Error() != `text is required for backend "smoke"` {
		t.Errorf("error %v", err)
	}
}
