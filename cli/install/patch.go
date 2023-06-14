package install

import (
	"os"

	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type patcher interface {
	isApplicable(ver version.Version) bool
	apply(srcPath string, verbose bool, logFile *os.File) error
}

type defaultPatchApplier struct {
	patch []byte
}

func (applier defaultPatchApplier) apply(srcPath string, verbose bool, logFile *os.File) error {
	err := util.ExecuteCommandStdin("git", verbose, logFile,
		srcPath, applier.patch, "apply")

	return err
}

type patchRange_1_to_2_6_1 struct {
	defaultPatchApplier
}

func (patchRange_1_to_2_6_1) isApplicable(ver version.Version) bool {
	return (ver.Major == 2 && ver.Minor == 6 && ver.Patch < 1) || ver.Major == 1
}

type patch_2_8_4 struct {
	defaultPatchApplier
}

func (patch_2_8_4) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 8 && ver.Patch == 4
}

type patch_2_10_beta struct {
	defaultPatchApplier
}

func (patch_2_10_beta) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 10 && ver.Release.Type == version.TypeBeta
}

type patch_2_10_0_rc1 struct {
	defaultPatchApplier
}

func (patch_2_10_0_rc1) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 10 && ver.Patch == 0 &&
		ver.Release.Type == version.TypeBeta && ver.Release.Num == 1
}

type patchRange_1_to_1_10_12 struct {
	defaultPatchApplier
}

func (patchRange_1_to_1_10_12) isApplicable(ver version.Version) bool {
	return ver.Major == 1 && ver.Minor == 10 && ver.Patch < 12
}

type patchRange_2_7_to_2_7_2 struct {
	defaultPatchApplier
}

func (patchRange_2_7_to_2_7_2) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 7 && ver.Patch < 2
}

type patchRange_2_7_2_to_2_7_4 struct {
	defaultPatchApplier
}

func (patchRange_2_7_2_to_2_7_4) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 7 && (ver.Patch > 1 && ver.Patch < 4)
}

type patchRange_2_8_1_to_2_8_4 struct {
	defaultPatchApplier
}

func (patchRange_2_8_1_to_2_8_4) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 8 && (ver.Patch > 0 && ver.Patch < 4)
}

type patchRange_1_to_1_10_14 struct {
	defaultPatchApplier
}

func (patchRange_1_to_1_10_14) isApplicable(ver version.Version) bool {
	return ver.Major == 1 && ver.Minor == 10 && ver.Patch < 14
}

type patchRange_1_10_14_to_1_10_16 struct {
	defaultPatchApplier
}

func (patchRange_1_10_14_to_1_10_16) isApplicable(ver version.Version) bool {
	return ver.Major == 1 && ver.Minor == 10 && (ver.Patch > 13 && ver.Patch < 16)
}

type patchRange_2_8_to_2_8_3 struct {
	defaultPatchApplier
}

func (patchRange_2_8_to_2_8_3) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor == 8 && ver.Patch < 3
}

type patchRange_2_to_2_8 struct {
	defaultPatchApplier
}

func (patchRange_2_to_2_8) isApplicable(ver version.Version) bool {
	return ver.Major == 2 && ver.Minor < 8
}
