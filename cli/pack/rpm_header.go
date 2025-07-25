package pack

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type filesInfo struct {
	BaseNames      []string
	DirNames       []string
	DirIndexes     []int32
	FileUserNames  []string
	FileGroupNames []string
	FileSizes      []int32
	FileModes      []int16
	FileINodes     []int32
	FileDevices    []int32
	FileMTimes     []int32
	FileLangs      []string
	FileRDevs      []int16
	FileLinkTos    []string
	FileFlags      []int32
	FileDigests    []string
}

// getRPMRelation accepts the string representation of package relation
// and returns its according code for rpm header.
func getRPMRelation(relation string) int32 {
	switch relation {
	case ">":
		return rpmSenseGreater
	case ">=":
		return rpmSenseGreater | rpmSenseEqual
	case "<":
		return rpmSenseLess
	case "<=":
		return rpmSenseLess | rpmSenseEqual
	case "=", "==":
		return rpmSenseEqual
	}

	return 0
}

// addDependenciesRPM writes all passed dependencies to the special rpm header.
func addDependenciesRPM(rpmHeader *rpmTagSetType, deps PackDependencies) {
	if len(deps) == 0 {
		return
	}

	var names []string
	var versions []string
	var relations []int32

	for _, dep := range deps {
		for _, r := range dep.Relations {
			names = append(names, dep.Name)
			relations = append(relations, getRPMRelation(r.Relation))
			versions = append(versions, r.Version)
		}

		if len(dep.Relations) == 0 {
			names = append(names, dep.Name)
			relations = append(relations, 0)
			versions = append(versions, "")
		}
	}

	rpmHeader.addTags([]rpmTagType{
		{
			ID: tagRequireName, Type: rpmTypeStringArray,
			Value: names,
		},
		{
			ID: tagRequireFlags, Type: rpmTypeInt32,
			Value: relations,
		},
		{
			ID: tagRequireVersion, Type: rpmTypeStringArray,
			Value: versions,
		},
	}...)
}

//go:embed templates/rpm_preinst.sh
var rpmPreInstScriptContent string

// addPreAndPostInstallScriptsRPM writes passed paths of pre-install
// and post-install scripts to the rpm header.
func addPreAndPostInstallScriptsRPM(rpmHeader *rpmTagSetType,
	preInst, postInst string,
) error {
	preInstScript := rpmPreInstScriptContent
	if preInst != "" {
		userPreInstBytes, err := os.ReadFile(preInst)
		if err != nil {
			return fmt.Errorf("error reading preinst file %q: %s", preInst, err)
		}
		preInstScript += "\n" + string(userPreInstBytes)
	}

	postInstScript := postInstScriptContent
	if postInst != "" {
		userPostInstBytes, err := os.ReadFile(postInst)
		if err != nil {
			return fmt.Errorf("error reading postinst file %q: %s", postInst, err)
		}
		postInstScript += "\n" + string(userPostInstBytes)
	}

	rpmHeader.addTags([]rpmTagType{
		{ID: tagPreIn, Type: rpmTypeString, Value: preInstScript},
		{ID: tagPostIn, Type: rpmTypeString, Value: postInstScript},
	}...)
	return nil
}

// genRpmHeader generates rpm headers.
func genRpmHeader(relPaths []string, cpioPath, compressedCpioPath, packageFilesDir string,
	cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, opts *config.CliOpts,
) (rpmTagSetType, error) {
	rpmHeader := rpmTagSetType{}

	// Compute payload digest.
	payloadDigestAlgo := hashAlgoSHA256
	payloadDigest, err := util.FileSHA256Hex(compressedCpioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get payload digest: %s", err)
	}

	cpioFileInfo, err := os.Stat(cpioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get payload size: %s", err)
	}
	payloadSize := cpioFileInfo.Size()

	// Generate fileinfo.
	filesInfo, err := getFilesInfo(relPaths, packageFilesDir, packCtx.RpmDeb.pkgFilesInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to get files info: %s", err)
	}

	versionString := getVersion(packCtx, opts, defaultVersion)

	ver, err := version.Parse(versionString)
	if err != nil {
		return nil, err
	}
	name, err := getPackageFileName(packCtx, opts, "", false)
	if err != nil {
		return nil, err
	}

	versionStr := strings.Join([]string{
		strconv.FormatUint(ver.Major, 10),
		strconv.FormatUint(ver.Minor, 10),
		strconv.FormatUint(ver.Patch, 10),
	}, ".")
	releaseStr := strconv.FormatUint(ver.Release.Num, 10)
	arch := getArch()

	rpmHeader.addTags([]rpmTagType{
		{ID: tagName, Type: rpmTypeString, Value: name},
		{ID: tagVersion, Type: rpmTypeString, Value: versionStr},
		{ID: tagRelease, Type: rpmTypeString, Value: releaseStr},
		{ID: tagSummary, Type: rpmTypeString, Value: ""},
		{ID: tagDescription, Type: rpmTypeString, Value: ""},

		{ID: tagLicense, Type: rpmTypeString, Value: "N/A"},
		{ID: tagGroup, Type: rpmTypeString, Value: "None"},
		{ID: tagOs, Type: rpmTypeString, Value: "linux"},
		{ID: tagArch, Type: rpmTypeString, Value: arch},

		{ID: tagPayloadFormat, Type: rpmTypeString, Value: "cpio"},
		{ID: tagPayloadCompressor, Type: rpmTypeString, Value: "gzip"},
		{ID: tagPayloadFlags, Type: rpmTypeString, Value: "5"},

		{ID: tagPreInProg, Type: rpmTypeString, Value: "/bin/sh"},
		{ID: tagPostInProg, Type: rpmTypeString, Value: "/bin/sh"},

		{ID: tagDirNames, Type: rpmTypeStringArray, Value: filesInfo.DirNames},
		{ID: tagBaseNames, Type: rpmTypeStringArray, Value: filesInfo.BaseNames},
		{ID: tagDirIndexes, Type: rpmTypeInt32, Value: filesInfo.DirIndexes},

		{ID: tagFileUserNames, Type: rpmTypeStringArray, Value: filesInfo.FileUserNames},
		{ID: tagFileGroupNames, Type: rpmTypeStringArray, Value: filesInfo.FileGroupNames},
		{ID: tagFileSizes, Type: rpmTypeInt32, Value: filesInfo.FileSizes},
		{ID: tagFileModes, Type: rpmTypeInt16, Value: filesInfo.FileModes},
		{ID: tagFileINodes, Type: rpmTypeInt32, Value: filesInfo.FileINodes},
		{ID: tagFileDevices, Type: rpmTypeInt32, Value: filesInfo.FileDevices},
		{ID: tagFileRDevs, Type: rpmTypeInt16, Value: filesInfo.FileRDevs},
		{ID: tagFileMTimes, Type: rpmTypeInt32, Value: filesInfo.FileMTimes},
		{ID: tagFileFlags, Type: rpmTypeInt32, Value: filesInfo.FileFlags},
		{ID: tagFileLangs, Type: rpmTypeStringArray, Value: filesInfo.FileLangs},
		{ID: tagFileDigests, Type: rpmTypeStringArray, Value: filesInfo.FileDigests},
		{ID: tagFileLinkTos, Type: rpmTypeStringArray, Value: filesInfo.FileLinkTos},

		{ID: tagSize, Type: rpmTypeInt32, Value: []int32{int32(payloadSize)}},
		{ID: tagPayloadDigest, Type: rpmTypeStringArray, Value: []string{payloadDigest}},
		{ID: tagPayloadDigestAlgo, Type: rpmTypeInt32, Value: []int32{int32(payloadDigestAlgo)}},
	}...)

	deps, err := parseAllDependencies(cmdCtx, packCtx)
	if err != nil {
		return nil, err
	}

	addDependenciesRPM(&rpmHeader, deps)
	err = addPreAndPostInstallScriptsRPM(&rpmHeader, packCtx.RpmDeb.PreInst,
		packCtx.RpmDeb.PostInst)

	return rpmHeader, err
}

// getFilesInfo returns the meta information about all items inside the passed
// directory needed for packing it into rpm headers.
func getFilesInfo(relPaths []string, dirPath string,
	pkgFiles map[string]packFileInfo,
) (filesInfo, error) {
	info := filesInfo{}

	for _, relPath := range relPaths {
		fullFilePath := filepath.Join(dirPath, relPath)
		fileInfo, err := os.Lstat(fullFilePath)
		if err != nil {
			return info, err
		}

		if fileInfo.Mode().IsRegular() {
			info.FileFlags = append(info.FileFlags, fileFlag)

			fileDigest, err := util.FileMD5Hex(fullFilePath)
			if err != nil {
				return info, fmt.Errorf("failed to get file MD5 hex: %s", err)
			}

			info.FileDigests = append(info.FileDigests, fileDigest)
		} else {
			info.FileFlags = append(info.FileFlags, dirFlag)
			info.FileDigests = append(info.FileDigests, emptyDigest)
		}

		fileDir := filepath.Dir(relPath)
		fileDir = fmt.Sprintf("/%s/", fileDir)
		dirIndex := addDirAndGetIndex(&info.DirNames, fileDir)
		info.DirIndexes = append(info.DirIndexes, int32(dirIndex))

		info.BaseNames = append(info.BaseNames, filepath.Base(relPath))
		info.FileMTimes = append(info.FileMTimes, int32(fileInfo.ModTime().Unix()))

		if packFileInfo, found := pkgFiles[relPath]; found {
			info.FileUserNames = append(info.FileUserNames, packFileInfo.owner)
			info.FileGroupNames = append(info.FileGroupNames, packFileInfo.group)
		} else {
			info.FileUserNames = append(info.FileUserNames, defaultFileUser)
			info.FileGroupNames = append(info.FileGroupNames, defaultFileGroup)
		}
		info.FileLangs = append(info.FileLangs, defaultFileLang)

		sysFileInfo, ok := fileInfo.Sys().(*syscall.Stat_t)
		if !ok {
			return info, fmt.Errorf("failed to get file info")
		}

		// If file is a symlink, it is needed to be the relative one.
		if fileInfo.Mode().Type() == fs.ModeSymlink {
			currentDir, err := os.Getwd()
			if err != nil {
				return filesInfo{}, err
			}
			os.Chdir(filepath.Dir(fullFilePath))
			path, err := filepath.EvalSymlinks(filepath.Base(fullFilePath))
			if err != nil {
				os.Chdir(currentDir)
				return filesInfo{}, err
			}
			info.FileLinkTos = append(info.FileLinkTos, path)
			os.Chdir(currentDir)
		} else {
			info.FileLinkTos = append(info.FileLinkTos, defaultFileLinkTo)
		}
		info.FileSizes = append(info.FileSizes, int32(sysFileInfo.Size))
		info.FileModes = append(info.FileModes, int16(sysFileInfo.Mode))
		info.FileINodes = append(info.FileINodes, int32(sysFileInfo.Ino))
		info.FileDevices = append(info.FileDevices, int32(sysFileInfo.Dev))
		info.FileRDevs = append(info.FileRDevs, int16(sysFileInfo.Rdev))
	}

	return info, nil
}

// addDirAndGetIndex accepts a slice of directory names and a file directory name,
// checks if the passed file directory name is already included in slice and returns
// its index. Otherwise, it appends this name to the slice and returns its index too.
func addDirAndGetIndex(dirNames *[]string, fileDir string) int {
	for i, dirName := range *dirNames {
		if dirName == fileDir {
			return i
		}
	}

	*dirNames = append(*dirNames, fileDir)
	return len(*dirNames) - 1
}

// getArch returns the architecture for an RPM package.
// Depends on runtime.GOARCH constant.
func getArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "386":
		return "i386"
	}
	log.Warn("The RPM package is going to be built with no architecture specified.")
	return "noarch"
}
