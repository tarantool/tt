package layout

import (
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/util"
)

// SingleInstanceLayout implements Layout for single instance applications.
type SingleInstanceLayout struct {
	baseDir string
	appName string
}

// NewSingleInstanceLayout creates new layout for single instance apps.
func NewSingleInstanceLayout(baseDir, appName string) (*SingleInstanceLayout, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("base directory cannot be empty")
	}
	if appName == "" {
		return nil, fmt.Errorf("application name cannot be empty")
	}

	return &SingleInstanceLayout{
		baseDir: baseDir,
		appName: appName,
	}, nil
}

// genFilePath generate file path.
func (layout SingleInstanceLayout) genFilePath(subdir, fileName string) string {
	dstDir := util.JoinPaths(layout.baseDir, subdir, layout.appName)
	return filepath.Join(dstDir, fileName)
}

// PidFile returns pid file path.
func (layout SingleInstanceLayout) PidFile(dir string) string {
	return layout.genFilePath(dir, layout.appName+".pid")
}

// LogFile returns log file path.
func (layout SingleInstanceLayout) LogFile(dir string) string {
	return layout.genFilePath(dir, layout.appName+".log")
}

// ConsoleSocket returns console file path.
func (layout SingleInstanceLayout) ConsoleSocket(dir string) string {
	return layout.genFilePath(dir, layout.appName+".control")
}

// DataDir returns data directory path.
func (layout SingleInstanceLayout) DataDir(dir string) string {
	return filepath.Dir(layout.genFilePath(dir, "0"))
}
