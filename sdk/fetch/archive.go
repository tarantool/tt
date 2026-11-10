package fetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// UnpackFile returns the content of a regular file of the .tar.gz archive
// targz: the first whose path, cleaned, is name or ends in "/" and name.
// Release archives put a program at the top or under a directory of their
// own, so "bin/tool" matches "./bin/tool", "bin/tool" and "tool-1.0/bin/tool",
// and "tool" matches each of them, but not "bin/mytool". An archive without
// such a file wraps ErrNotInArchive. limit bounds the file, 0 or less being
// DefaultLimit, and a larger one wraps ErrTooLarge. Nothing of the archive is
// written anywhere, so its paths need no sanitizing.
func UnpackFile(targz []byte, name string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}

	name = path.Clean(name)

	compressed, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", name, err)
	}

	archive := tar.NewReader(compressed)

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("unpack %s: %w", name, ErrNotInArchive)
		}

		if err != nil {
			return nil, fmt.Errorf("unpack %s: %w", name, err)
		}

		if header.Typeflag != tar.TypeReg || !matchPath(header.Name, name) {
			continue
		}

		if header.Size > limit {
			return nil, fmt.Errorf("unpack %s: %w: more than %d bytes", name, ErrTooLarge, limit)
		}

		data, err := io.ReadAll(io.LimitReader(archive, limit+1))
		if err != nil {
			return nil, fmt.Errorf("unpack %s: %w", name, err)
		}

		if int64(len(data)) > limit {
			return nil, fmt.Errorf("unpack %s: %w: more than %d bytes", name, ErrTooLarge, limit)
		}

		return data, nil
	}
}

// matchPath reports whether the path of a member of an archive, cleaned, is
// name or ends in "/" and name.
func matchPath(member, name string) bool {
	member = path.Clean(member)

	return member == name || strings.HasSuffix(member, "/"+name)
}
