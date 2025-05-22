package running

import (
	"github.com/tarantool/tt/cli/process_utils"
)

// InstanceDelimiter is the delimiter of the app and instance name.
const InstanceDelimiter = ':'

var getStatus = Status

// extractInstanceNames returns the names of instances, that satisfy the filter.
func extractInstanceNames(instances []InstanceCtx,
	filter func(*InstanceCtx) bool,
) []string {
	validNames := make([]string, 0)
	for _, instance := range instances {
		if filter(&instance) {
			validNames = append(validNames, GetAppInstanceName(instance))
		}
	}
	return validNames
}

// extractAppNames returns the names of applications, that
// have an instance, that satisfy the filter.
func extractAppNames(instances []InstanceCtx,
	filter func(*InstanceCtx) bool,
) []string {
	validAppNames := make([]string, 0)
	isAnyValid := false
	for i, instance := range instances {
		isAnyValid = isAnyValid || filter(&instance)
		if i+1 == len(instances) || instance.AppName != instances[i+1].AppName {
			if isAnyValid {
				validAppNames = append(validAppNames, instance.AppName)
			}
			isAnyValid = false
		}
	}
	return validAppNames
}

// IsInstanceActive returns true if the instance have running status.
func IsInstanceActive(instance *InstanceCtx) bool {
	return getStatus(instance).Code == process_utils.ProcessRunningCode
}

// IsInstanceInactive return true if the instance have not running status.
func IsInstanceInactive(instance *InstanceCtx) bool {
	return !IsInstanceActive(instance)
}

// ExtractActiveInstanceNames returns the names of running instances.
func ExtractActiveInstanceNames(instances []InstanceCtx) []string {
	return extractInstanceNames(instances, IsInstanceActive)
}

// ExtractInactiveInstanceNames returns the names of not running instances.
func ExtractInactiveInstanceNames(instances []InstanceCtx) []string {
	return extractInstanceNames(instances, IsInstanceInactive)
}

// ExtractActiveAppNames returns the names of applications,
// that have a running instance.
func ExtractActiveAppNames(instances []InstanceCtx) []string {
	return extractAppNames(instances, IsInstanceActive)
}

// ExtractInactiveAppNames returns the names of applications,
// that have a not running instance.
func ExtractInactiveAppNames(instances []InstanceCtx) []string {
	return extractAppNames(instances, IsInstanceInactive)
}

// ExtractInstanceNames returns the names of instances.
func ExtractInstanceNames(instances []InstanceCtx) []string {
	return extractInstanceNames(instances, func(_ *InstanceCtx) bool {
		return true
	})
}

// ExtractAppNames returns the names of apps.
func ExtractAppNames(instances []InstanceCtx) []string {
	return extractAppNames(instances, func(_ *InstanceCtx) bool {
		return true
	})
}
