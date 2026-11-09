package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
)

var errArtifactChecksum = errors.New("archive checksum mismatch")

// fetchArtifact writes the archive of artifact from source into the file at
// destFilePath and checks it against the digest the source listed, if any.
// A failed fetch leaves no file behind.
func fetchArtifact(ctx context.Context, source bundle.Source, artifact bundle.Artifact,
	destFilePath string,
) error {
	file, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destFilePath, err)
	}

	digest := sha256.New()

	err = source.Fetch(ctx, artifact, io.MultiWriter(file, digest))

	closeErr := file.Close()
	if err == nil && closeErr != nil {
		err = fmt.Errorf("failed to close destination file %s: %w", destFilePath, closeErr)
	}

	if err == nil && artifact.SHA256 != "" {
		actual := hex.EncodeToString(digest.Sum(nil))
		if !strings.EqualFold(actual, artifact.SHA256) {
			err = fmt.Errorf("%w: %s has SHA-256 %s, %s lists %s", errArtifactChecksum,
				artifact.Name, actual, source.Name(), artifact.SHA256)
		}
	}

	if err != nil {
		_ = os.Remove(destFilePath)

		return err
	}

	return nil
}
