package cmd

import (
	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// installChecks is what tt package install --integrity-check checks an
// archive with: the integrity provider of the distribution and the public
// key of the flag.
type installChecks struct {
	publicKey string
}

var _ install.Integrity = installChecks{publicKey: ""}

// installIntegrity returns the checks of an install with the public key at
// publicKey, or nil, which installs without checks, when there is none.
func installIntegrity(publicKey string) install.Integrity {
	if publicKey == "" {
		return nil
	}

	return installChecks{publicKey: publicKey}
}

// VerifyTree checks that the tree in dir is signed as a whole.
func (checks installChecks) VerifyTree(dir string) error {
	return integrityprovider.Get().VerifyTree(checks.publicKey, dir)
}

// VerifyFile checks file with the signed hashes files of root.
func (checks installChecks) VerifyFile(root, file string) error {
	ctx, err := integrityprovider.Get().InitializeIntegrityCheck(checks.publicKey, root)
	if err != nil {
		return err
	}

	reader, err := ctx.Repository.Read(file)
	if err != nil {
		return err
	}

	return reader.Close() //nolint:wrapcheck // install names what it checks.
}
