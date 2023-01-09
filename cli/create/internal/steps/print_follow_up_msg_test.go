package steps

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestPrintFollowUpMessage(t *testing.T) {
	var buffer bytes.Buffer
	printFollowUpMsgStep := PrintFollowUpMessage{
		Writer: &buffer,
	}
	err := printFollowUpMsgStep.Run(&create_ctx.CreateCtx{
		CliOpts: &config.CliOpts{},
	}, &app_template.TemplateCtx{
		Vars: map[string]string{
			"name": "app1",
		},
		Manifest: app_template.TemplateManifest{
			FollowUpMessage: "App name is {{.name}}",
		},
		IsManifestPresent: true,
	})
	require.NoError(t, err)
	msg, err := buffer.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "App name is app1\n", msg)
}

func TestPrintFollowUpMessageError(t *testing.T) {
	var buffer bytes.Buffer
	printFollowUpMsgStep := PrintFollowUpMessage{
		Writer: &buffer,
	}
	err := printFollowUpMsgStep.Run(&create_ctx.CreateCtx{
		CliOpts: &config.CliOpts{},
	}, &app_template.TemplateCtx{
		Vars: map[string]string{},
		Manifest: app_template.TemplateManifest{
			FollowUpMessage: "App name is {{.name}}",
		},
		IsManifestPresent: true,
	})
	require.Error(t, err)
}
