package running

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathBuilder(t *testing.T) {
	builder := NewArtifactsPathBuilder("/var", "app1")
	assert.Equal(t, "/var/app1", builder.Make())

	builder = builder.WithPath("rundir")
	assert.Equal(t, "/var/rundir/app1", builder.Make())

	builder = builder.WithPath("/rundir")
	assert.Equal(t, "/rundir/app1", builder.Make())

	builder = builder.WithTarantoolctlLayout(true)
	assert.Equal(t, "/rundir", builder.Make())

	builder = builder.WithPath("rundir")
	assert.Equal(t, "/var/rundir", builder.Make())

	// For multi instance app, application sub-dir is always used.
	builder = builder.WithPath("rundir").ForInstance("router")
	assert.Equal(t, "/var/rundir/app1/router", builder.Make())

	builder = builder.WithTarantoolctlLayout(false)
	assert.Equal(t, "/var/rundir/app1/router", builder.Make())
}
