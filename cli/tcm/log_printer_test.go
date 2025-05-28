package tcm

import (
	"bytes"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

func TestNewLogFormatter(t *testing.T) {
	defaultNoColor := color.NoColor
	tests := map[string]struct {
		noFormat bool
		noColor  bool
	}{
		"noFormat and noColor": {
			noFormat: true,
			noColor:  true,
		},
		"noFormat and color": {
			noFormat: true,
			noColor:  false,
		},
		"format and noColor": {
			noFormat: false,
			noColor:  true,
		},
		"format and color": {
			noFormat: false,
			noColor:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			out := &bytes.Buffer{}

			lf := NewLogFormatter(tt.noFormat, tt.noColor, out)
			require.NotNil(t, lf, "NewLogFormatter should return a non-nil object")
			require.Implements(t, (*LogPrinter)(nil), lf,
				"NewLogFormatter should return an object implementing LogPrinter")

			require.Equal(t, out, lf.out, "Internal out field mismatch")
			require.Equal(t, tt.noFormat, lf.noFormat, "Internal noFormat field mismatch")

			if lf.noColor || lf.noFormat {
				require.Equal(t, true, lf.noColor, "Internal noColor field should be true")
			} else {
				require.Equal(t, tt.noColor, lf.noColor, "Internal noColor field mismatch")
			}

			if !tt.noFormat {
				require.NotNil(t, lf.color, "NewLogFormatter should return a non-nil color picker")
			}
		})
	}

	color.NoColor = defaultNoColor
}
