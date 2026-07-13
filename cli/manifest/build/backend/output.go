package backend

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyOutputs copies each declared output from cwd into outputDir, flattened to
// its basename (matching how the loader looks up <namespace>/<name>). It
// creates outputDir first. A listed file that does not exist after the build is
// an error — the build claimed an output it did not produce. Shared by the
// shell and make backends and run only after a zero-exit build.
func copyOutputs(outputDir, cwd string, outputs []string) error {
	if err := os.MkdirAll(outputDir, dirPerm); err != nil {
		return fmt.Errorf("create output directory %q: %w", outputDir, err)
	}

	for _, entry := range outputs {
		src := filepath.Join(cwd, entry)
		dst := filepath.Join(outputDir, filepath.Base(entry))

		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy declared output %q: %w", entry, err)
		}
	}

	return nil
}

// copyFile copies src to dst, preserving the source's permission bits and
// creating dst's parent directory. It fails if src does not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()

		return err
	}

	return out.Close()
}
