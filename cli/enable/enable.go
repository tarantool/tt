package enable

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// defaultDirPermissions is rights used to create folders.
// 0755 - drwxr-xr-x
// We need to give permission for all to execute
// read,write for user and only read for others.
const defaultDirPermissions = 0o755

// Enable creates a symbolic link in 'instances_enabled' directory
// to a script or an application directory.
func Enable(path string, cliOpts *config.CliOpts) error {
	var err error

	if _, err := os.Stat(cliOpts.Env.InstancesEnabled); os.IsNotExist(err) {
		err = os.MkdirAll(cliOpts.Env.InstancesEnabled, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %q\n Error: %s",
				cliOpts.Env.InstancesEnabled, err)
		}
		log.Infof("Instances enabled directory is created: %q", cliOpts.Env.InstancesEnabled)
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}

	pathInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot get info of %q: %s", path, err)
	}

	if pathInfo.IsDir() {
		if !util.IsApp(path) {
			return fmt.Errorf("directory %q is not an application", path)
		}
	} else if filepath.Ext(path) != ".lua" {
		return fmt.Errorf("script %q does not have '.lua' extension", path)
	}

	err = util.CreateSymlink(path, filepath.Join(cliOpts.Env.InstancesEnabled,
		filepath.Base(path)), true)
	if err != nil {
		return err
	}
	log.Infof("Symbolic link for %q was created in instances enabled directory", path)

	return nil
}
