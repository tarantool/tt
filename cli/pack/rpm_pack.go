package pack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

//nolint - some links from description are too long
/**
 *
 *  Many thanks to @knazarov, who wrote packing in RPM in Lua a long time ago
 *  This code can be found here
 *  https://github.com/tarantool/cartridge-cli/blob/cafd75a5c8ddfdb93ef8290d6e4ebd5d83e4c46e/cartridge-cli.lua#L1814
 *
 *  RPM file is a binary file format, consisting of metadata in the form of
 *  key-value pairs and then a gzipped cpio archive (of SVR-4 variety).
 *
 *  Documentation on the binary format can be found here:
 *  - http://ftp.rpm.org/max-rpm/s1-rpm-file-format-rpm-file-format.html
 *
 *  Also I've found this explanatory blog post to be of great help:
 *  - https://blog.bethselamin.de/posts/argh-pm.html
 *
 *  Here's what the layout looks like:
 *
 *  +-----------------------+
 *  |                       |
 *  |     Lead (legacy)     |
 *  |                       |
 *  +-----------------------+
 *  |                       |
 *  |   Signature Header    |
 *  |                       |
 *  +-----------------------+
 *  |                       |
 *  |        Header         |
 *  |                       |
 *  +-----------------------+
 *  |                       |
 *  |                       |
 *  |    Data (cpio.gz)     |
 *  |                       |
 *  |                       |
 *  +-----------------------+
 *
 *  Both signature sections have the same format: a set of typed
 *  key-value pairs.
 *
 *  While debugging, I used rpm-dissecting tool from mkrepo:
 *  - https://github.com/tarantool/mkrepo/blob/master/mkrepo.py
 *
 */

// packRpm creates an RPM archive in resPackagePath
// that contains files from packageDir.
func packRpm(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, opts *config.CliOpts, packageDir,
	resPackagePath string) error {
	var err error

	relPaths, err := getSortedRelPaths(packageDir)
	if err != nil {
		return fmt.Errorf("Failed to get sorted package files list: %s", err)
	}

	log.Info("Creating data section")

	cpioPath := filepath.Join(packageDir, "cpio")
	if err := packCpio(relPaths, cpioPath, packageDir); err != nil {
		return fmt.Errorf("Failed to pack CPIO: %s", err)
	}

	compresedCpioPath := filepath.Join(packageDir, "cpio.gz")
	if err := CompressGzip(cpioPath, compresedCpioPath); err != nil {
		return fmt.Errorf("Failed to compress CPIO: %s", err)
	}

	log.Info("Generating header section")

	rpmHeader, err := genRpmHeader(relPaths, cpioPath, compresedCpioPath, packageDir,
		cmdCtx, packCtx, opts)
	if err != nil {
		return fmt.Errorf("Failed to gen RPM header: %s", err)
	}

	packedHeader, err := packTagSet(rpmHeader, headerImmutable)
	if err != nil {
		return fmt.Errorf("Failed to pack RPM header: %s", err)
	}

	// Write header to file.
	rpmHeaderFilePath := filepath.Join(packageDir, "header")
	rpmHeaderFile, err := os.Create(rpmHeaderFilePath)
	if err != nil {
		return fmt.Errorf("Failed to create RPM body file: %s", err)
	}
	defer rpmHeaderFile.Close()

	if _, err := io.Copy(rpmHeaderFile, packedHeader); err != nil {
		return fmt.Errorf("Failed to write RPM lead to file: %s", err)
	}

	// Create body file = header + compressedCpio.
	rpmBodyFilePath := filepath.Join(packageDir, "body")
	if err := util.MergeFiles(rpmBodyFilePath, rpmHeaderFilePath, compresedCpioPath); err != nil {
		return fmt.Errorf("Failed to concat RPM header with compressed payload: %s", err)
	}

	log.Info("Computing a signature")

	// Compute signature.
	signature, err := genSignature(rpmBodyFilePath, rpmHeaderFilePath, cpioPath)
	if err != nil {
		return fmt.Errorf("Failed to gen RPM signature: %s", err)
	}

	packedSignature, err := packTagSet(*signature, headerSignatures)
	if err != nil {
		return fmt.Errorf("Failed to pack RPM header: %s", err)
	}
	alignData(packedSignature, 8)

	log.Info("Computing lead section")

	// Compute lead.
	name, err := getPackageName(packCtx, opts, "", false)
	if err != nil {
		return err
	}
	lead := genRpmLead(name)
	if err := util.ConcatBuffers(lead, packedSignature); err != nil {
		return err
	}

	// Create lead file.
	leadFilePath := filepath.Join(packageDir, "lead")
	leadFile, err := os.Create(leadFilePath)
	if err != nil {
		return fmt.Errorf("Failed to create RPM lead file: %s", err)
	}

	if _, err := io.Copy(leadFile, lead); err != nil {
		return fmt.Errorf("Failed to write RPM lead to file: %s", err)
	}

	// Create RPM file.
	err = util.MergeFiles(resPackagePath,
		leadFilePath,
		rpmBodyFilePath,
	)
	if err != nil {
		return fmt.Errorf("Failed to write result RPM file: %s", err)
	}

	return nil
}

// getSortedRelPaths collect all paths into a slice, starting from the passed directory,
// sorts it and returns.
func getSortedRelPaths(srcDir string) ([]string, error) {
	var files []string

	err := filepath.Walk(srcDir, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		filePath, err = filepath.Rel(srcDir, filePath)
		if err != nil {
			return err
		}

		// System dirs shouldn't be added to the paths list.
		if _, isSystem := systemDirs[filePath]; !isSystem {
			files = append(files, filePath)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

// packValues puts all passed values into a buffer and returns it.
func packValues(values ...interface{}) *bytes.Buffer {
	buf := bytes.NewBuffer(nil)

	for _, v := range values {
		binary.Write(buf, binary.BigEndian, v)
	}

	return buf
}

// alignData aligns all data in buffer according to the passed boundaries.
func alignData(data *bytes.Buffer, boundaries int) {
	dataLen := data.Len()

	if dataLen%boundaries != 0 {
		alignedDataLen := (dataLen/boundaries + 1) * boundaries

		missedBytesNum := alignedDataLen - dataLen

		paddingBytes := make([]byte, missedBytesNum)
		data.Write(paddingBytes)
	}
}
