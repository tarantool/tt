package steps

import (
	"io"

	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/templates"
)

type PrintFollowUpMessage struct {
	// Writer is used to write follow-up message.
	Writer io.Writer
}

// Run prints application template follow-up message.
func (printFollowUpMsgStep PrintFollowUpMessage) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	if templateCtx.IsManifestPresent && templateCtx.Manifest.FollowUpMessage != "" &&
		!createCtx.SilentMode {

		templateEngine := templates.NewDefaultEngine()
		followUpText, err := templateEngine.RenderText(templateCtx.Manifest.FollowUpMessage,
			templateCtx.Vars)
		if err != nil {
			return err
		}

		printFollowUpMsgStep.Writer.Write([]byte(followUpText + "\n"))
	}

	return nil
}
