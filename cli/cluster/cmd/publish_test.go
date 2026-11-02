package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	clustercmd "github.com/tarantool/tt/v3/cli/cluster/cmd"
)

// TestPublishApp_FileWithIntegrity_Errors verifies that PublishApp returns an
// error when the publisher factory does not support file publishing with
// integrity data (e.g. IntegrityDataPublisherFactory).
func TestPublishApp_FileWithIntegrity_Errors(t *testing.T) {
	src := []byte(`groups:
  g:
    replicasets:
      r:
        instances:
          inst: {}
`)

	ctx := clustercmd.PublishCtx{
		Force: true, // skip validation.
		Publishers: sdkcluster.NewFactory(
			sdkcluster.WithIntegrity(sdkcluster.IntegrityOptions{}),
		),
		Collectors: sdkcluster.NewFactory(),
		Src:        src,
	}

	// A config that points to no storage is published into the file itself.
	// IntegrityDataPublisherFactory.NewFile returns an error, so this must fail.
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	err := clustercmd.PublishApp(ctx, path, "")
	require.ErrorContains(t, err, "publishing into a file with integrity data is not supported")
}

// TestPublishApp_Confirm verifies that PublishApp asks PublishCtx.Confirm
// whether to publish, naming the source and the file the config goes into,
// once the config has been checked: a config the checks refuse is not asked
// about, and nothing is written unless the answer is yes.
func TestPublishApp_Confirm(t *testing.T) {
	original := []byte("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          inst: {}\n")
	published := []byte("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          inst: {}\n          other: {}\n")
	errRefused := errors.New("refused")

	cases := []struct {
		name     string
		answer   bool
		answered error
		refused  error
		asked    bool
		want     []byte
	}{
		{name: "yes", answer: true, asked: true, want: published},
		{name: "no", answer: false, asked: true, want: original},
		{name: "no answer", answered: errRefused, asked: true, want: original},
		{name: "refused by the checks", answer: true, refused: errRefused, want: original},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, original, 0o600))

			var questions []string

			ctx := clustercmd.PublishCtx{
				Force:      true, // skip validation.
				Publishers: sdkcluster.NewFactory(),
				Collectors: sdkcluster.NewFactory(),
				Src:        published,
				Source:     "src.yaml",
				CheckTopology: func([]string) error {
					return test.refused
				},
				Confirm: func(question string) (bool, error) {
					questions = append(questions, question)

					return test.answer, test.answered
				},
			}

			err := clustercmd.PublishApp(ctx, path, "")

			switch {
			case test.refused != nil:
				require.ErrorIs(t, err, test.refused)
			case test.answered != nil:
				require.ErrorIs(t, err, test.answered)
			default:
				require.NoError(t, err)
			}

			if test.asked {
				assert.Equal(t, []string{"Confirm publish of src.yaml, which replaces the " +
					"cluster config in " + path}, questions)
			} else {
				assert.Empty(t, questions)
			}

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, string(test.want), string(data))
		})
	}
}
