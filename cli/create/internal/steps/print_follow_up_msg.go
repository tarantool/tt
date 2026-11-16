package steps

import (
	"io"

	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/templates"
)

type PrintFollowUpMessage struct {
	// Writer is used to write follow-up message.
	Writer io.Writer
}

// Run prints application template follow-up message. It is printed with
// -s as well: the non-interactive mode only stops asking for variables, and
// the message tells the next step.
func (printFollowUpMsgStep PrintFollowUpMessage) Run(_ *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx,
) error {
	if templateCtx.IsManifestPresent && templateCtx.Manifest.FollowUpMessage != "" {
		templateEngine := templates.NewDefaultEngine()

		followUpText, err := templateEngine.RenderText(templateCtx.Manifest.FollowUpMessage,
			templateCtx.Vars)
		if err != nil {
			return err
		}

		_, _ = printFollowUpMsgStep.Writer.Write([]byte(followUpText + "\n"))
	}

	return nil
}
