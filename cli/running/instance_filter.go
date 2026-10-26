package running

import (
	"github.com/tarantool/tt/v3/cli/process_utils"
)

// InstanceDelimiter is the delimiter of the app and instance name.
const InstanceDelimiter = ':'

var getStatus = Status

// IsInstanceActive returns true if the instance have running status.
func IsInstanceActive(instance *InstanceCtx) bool {
	return getStatus(instance).Code == process_utils.ProcessRunningCode
}

// IsInstanceInactive return true if the instance have not running status.
func IsInstanceInactive(instance *InstanceCtx) bool {
	return !IsInstanceActive(instance)
}
