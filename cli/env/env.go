package env

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/config"
)

// CreateEnvString generates environment variables for 'tarantool' and 'tt' installed using 'tt'.
func CreateEnvString(cliOpts *config.CliOpts) string {
	binDir := cliOpts.Env.BinDir
	path := os.Getenv("PATH")
	if !strings.Contains(path, binDir) {
		path = binDir + ":" + path
	}

	tarantoolDir := filepath.Join(cliOpts.Env.IncludeDir, "include")

	return fmt.Sprintf("export %s=%s\nexport %s=%s\n", "PATH", path, "TARANTOOL_DIR", tarantoolDir)
}
