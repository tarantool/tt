package running

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func TestGetInstancesFromYML(t *testing.T) {
	instances, err := getInstancesFromYML(filepath.Join("testdata", "app1"), "")
	require.NoError(t, err)
	require.Equal(t, 6, len(instances))

	for _, instanceName := range []string{"router", "s1-master", "s1-replica",
		"s2-master", "s2-replica", "stateboard"} {
		assert.NotEqual(t, -1, slices.IndexFunc(instances, func(instanceCtx InstanceCtx) bool {
			return instanceCtx.InstName == instanceName
		}))
	}
}
