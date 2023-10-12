package layout

import (
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/util"
)

// MultiInstLayout implements Layout interface for multi-instance applications.
type MultiInstLayout struct {
	baseDir      string
	appName      string
	instanceName string
}

// NewMultiInstLayout creates new multi-instance layout.
func NewMultiInstLayout(baseDir, appName, instanceName string) (*MultiInstLayout, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("base directory cannot be empty")
	}
	if appName == "" {
		return nil, fmt.Errorf("application name cannot be empty")
	}
	if instanceName == "" {
		return nil, fmt.Errorf("instance name cannot be empty")
	}

	return &MultiInstLayout{
		baseDir:      baseDir,
		appName:      appName,
		instanceName: instanceName,
	}, nil
}

// genFilePath generate file path.
func (layout MultiInstLayout) genFilePath(subdir, fileName string) string {
	var dstDir string
	if filepath.IsAbs(subdir) {
		dstDir = util.JoinPaths(layout.baseDir, subdir, layout.appName, layout.instanceName)
	} else {
		dstDir = util.JoinPaths(layout.baseDir, subdir, layout.instanceName)
	}
	return filepath.Join(dstDir, fileName)
}

// PidFile returns pid file path.
func (layout MultiInstLayout) PidFile(dir string) string {
	return layout.genFilePath(dir, "tt.pid")
}

// LogFile returns log file path.
func (layout MultiInstLayout) LogFile(dir string) string {
	return layout.genFilePath(dir, "tt.log")
}

// ConsoleSocket returns console file path.
func (layout MultiInstLayout) ConsoleSocket(dir string) string {
	return layout.genFilePath(dir, "tarantool.control")
}

// DataDir returns data directory path.
func (layout MultiInstLayout) DataDir(dir string) string {
	return filepath.Dir(layout.genFilePath(dir, "0"))
}
