package connect

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/formatter"
)

// setFormatPrefix is a prefix for a set output format interactive option.
const setFormatPrefix = "\\set output "

// setFormatShortPrefix is a short command for a set output format interactive option.
const setFormatShortPrefix = "\\x"

// setTableDialectPrefix is a short prefix for a set output table dialect interactive option.
const setTableDialectPrefix = "\\set table_format "

// setPseudoGraphicsEnable is an interactive option for pseudo graphics enabling of
// table/ttalbe output formats.
const setPseudoGraphicsEnable = "\\xG"

// setPseudoGraphicsEnable is an interactive option for pseudo graphics disabling of
// table/ttalbe output formats.
const setPseudoGraphicsDisable = "\\xg"

// setTableColWidthShortPrefix is a short prefix for table column width interactive option.
const setTableColWidthShortPrefix = "\\xw "

// setTableColWidthShortPrefix is a prefix for table column width interactive option.
const setTableColWidthPrefix = "\\set table_column_width "

// helpOptionHandler prints help of tt connect interactive option.
func helpOptionHandler() {
	var help string = `
  To get help, see the Tarantool manual at https://tarantool.io/en/doc/
  To start the interactive Tarantool tutorial, type 'tutorial()' here.

  This help is expanded with additional backslash commands
  because tt connect is using.

  Available backslash commands:

  \set language <language>        -- set language lua or sql
  \set output <format>            -- set output format yaml, lua, table, ttable
  \x[y,l,t,T]                     -- set output format yaml, lua, table, ttable
  \x                              -- switches output format cyclically
  \x[g,G]                         -- disables/enables pseudographics for tables
  \set table_column_width <width> -- max column width for table/ttable
  \xw                             -- max column width for table/ttable
  \table_format <format>          -- tables format markdown, jira or default
  \help                           -- show this screen
  \quit                           -- quit interactive console
  `
	fmt.Println(help)
}

// handleLanguageOption handles language interactive option.
func handleLanguageOption(trimmed string, console *Console) {
	newLang := strings.TrimPrefix(trimmed, setLanguagePrefix)
	if lang, ok := ParseLanguage(newLang); ok {
		if err := ChangeLanguage(console.conn, lang); err != nil {
			log.Warnf("Failed to change language: %s", err)
		} else {
			console.language = lang
			log.Infof("Selected language: %s", console.language.String())
		}
	} else {
		log.Warnf("Unsupported language: %s", newLang)
	}
}

// handleWidthOption handles width interactive option for table/ttable output formats.
func handleWidthOption(trimmed string, console *Console, formatterOpts *formatter.Opts) {
	var maxWidthStr string
	if strings.HasPrefix(trimmed, strings.TrimSpace(setTableColWidthShortPrefix)) {
		maxWidthStr = strings.TrimPrefix(trimmed, setTableColWidthShortPrefix)
	} else if strings.HasPrefix(trimmed, strings.TrimSpace(setTableColWidthPrefix)) {
		maxWidthStr = strings.TrimPrefix(trimmed, setTableColWidthPrefix)
	} else {
		panic("there is no pattern cases for get width value")
	}
	valCasted, err := strconv.ParseInt(maxWidthStr, 10, 64)
	if err == nil && valCasted >= 0 {
		formatterOpts.ColWidthMax = int(valCasted)
		if formatterOpts.ColWidthMax > 0 {
			log.Infof("Selected max width: %v", formatterOpts.ColWidthMax)
		} else {
			log.Info("Selected max width: disabled")
		}
	} else {
		log.Errorf("Max width must be non-negative number, but you gave: %v", maxWidthStr)
	}
}

// handlePseudoGraphicsOption handles pseudo graphics enable/disable interactive option
// for table/ttable output formats.
func handlePseudoGraphicsOption(trimmed string, console *Console,
	formatterOpts *formatter.Opts) {
	if console.outputFormat == formatter.TableFormat ||
		console.outputFormat == formatter.TTableFormat {
		if trimmed == setPseudoGraphicsEnable {
			formatterOpts.NoGraphics = false
			log.Info("Pseudo graphics is enabled now")
		} else if trimmed == setPseudoGraphicsDisable {
			formatterOpts.NoGraphics = true
			log.Info("Pseudo graphics is disabled now")
		} else {
			panic("there is no pattern cases for handling pseudo graphics enabling/disabling")
		}
	} else {
		log.Error("Pseudo graphics enabling/disabling " +
			"only allowed for table and ttable output formats")
	}
}

// handleFormatOption handles output interactive option.
func handleFormatOption(trimmed string, executorEval *string,
	console *Console, formatterOpts *formatter.Opts) {
	var newFormat string
	if strings.HasPrefix(trimmed, setFormatPrefix) {
		newFormat = strings.TrimPrefix(trimmed, setFormatPrefix)
	}
	if strings.HasPrefix(trimmed, setFormatShortPrefix) {
		if trimmed == setFormatShortPrefix {
			if console.outputFormat == formatter.DefaultFormat {
				console.outputFormat = formatter.YamlFormat
				formatterOpts.TransposeMode = formatter.IsTTableFormat(console.outputFormat)
			}
			console.outputFormat = 1 + (console.outputFormat % 4)
			formatterOpts.TransposeMode = formatter.IsTTableFormat(console.outputFormat)
			trimmed = console.outputFormat.String()
		}
		newFormat = strings.TrimPrefix(trimmed, setFormatShortPrefix)
	}
	if outputFormat, ok := formatter.ParseFormat(newFormat); ok {
		if outputFormat == formatter.TableFormat || outputFormat == formatter.TTableFormat {
			*executorEval = consoleEvalFuncBodyTblsFmt
			console.outputFormat = outputFormat
			formatterOpts.TransposeMode = formatter.IsTTableFormat(console.outputFormat)
			log.Infof("Selected output format: %s", console.outputFormat.String())
		} else {
			*executorEval = consoleEvalFuncBody
			console.outputFormat = outputFormat
			formatterOpts.TransposeMode = formatter.IsTTableFormat(console.outputFormat)
			log.Infof("Selected output format: %s", console.outputFormat.String())
		}
	} else {
		log.Warnf("Unsupported output format: %s", newFormat)
	}
}

// handleTableDialectOption handles table dialect interactive option
// for table/ttable output formats.
func handleTableDialectOption(trimmed string, formatterOpts *formatter.Opts) {
	newTableDialect := strings.TrimPrefix(trimmed, setTableDialectPrefix)
	if tableDialect, ok := formatter.ParseTableDialect(newTableDialect); ok {
		formatterOpts.TableDialect = tableDialect
		if formatterOpts.TableDialect != formatter.DefaultTableDialect &&
			formatterOpts.ColWidthMax > 0 {
			formatterOpts.ColWidthMax = 0
			log.Info("Selected max width: disabled")
		}
		log.Infof("Selected table dialect: %s", formatterOpts.TableDialect.String())
	} else {
		log.Warnf("Unsupported table dialect: %s", newTableDialect)
	}
}

// handleInteractiveOption handles slash options for interactive console and returns
// true if slash options detected in user input.
func handleInteractiveOption(in string, executorEval *string,
	console *Console, formatterOpts *formatter.Opts) bool {
	trimmed := strings.TrimSpace(in)

	// Helper case handling.
	if trimmed == "\\help" || in == "help" ||
		trimmed == "\\" || trimmed == "\\?" {
		helpOptionHandler()
		return true
	}

	// Language case handling.
	if strings.HasPrefix(trimmed, setLanguagePrefix) {
		handleLanguageOption(trimmed, console)
		return true
	}

	// Table width case handling.
	if strings.HasPrefix(trimmed, strings.TrimSpace(setTableColWidthShortPrefix)) ||
		strings.HasPrefix(trimmed, strings.TrimSpace(setTableColWidthPrefix)) {
		if formatterOpts.TableDialect != formatter.DefaultTableDialect {
			log.Error("Max width option only supports for default table dialect, " +
				"not for jira or markdown.")
			return true
		}
		if console.outputFormat != formatter.TableFormat &&
			console.outputFormat != formatter.TTableFormat {
			log.Error("Max width option only supports for table and ttable output formats.")
			return true
		}
		handleWidthOption(trimmed, console, formatterOpts)
		return true
	}

	// Table graphics enable/disable case handling.
	if trimmed == setPseudoGraphicsDisable || trimmed == setPseudoGraphicsEnable {
		if formatterOpts.TableDialect != formatter.DefaultTableDialect {
			log.Error("Pseudo graphics enabling/disabling supports only for default table dialect")
			return true
		}
		handlePseudoGraphicsOption(trimmed, console, formatterOpts)
		return true
	}

	// Output format case handling.
	if trimmed == strings.TrimSpace(setFormatPrefix) {
		log.Error("Specify output format: yaml, lua, table or ttable.")
		return true
	}
	if strings.HasPrefix(trimmed, setFormatPrefix) ||
		strings.HasPrefix(trimmed, setFormatShortPrefix) {
		handleFormatOption(trimmed, executorEval, console, formatterOpts)
		return true
	}

	// Table dialect case handling.
	if trimmed == strings.TrimSpace(setTableDialectPrefix) {
		log.Error("Specify table dialect: default, markdown or jira")
		return true
	}
	if strings.HasPrefix(trimmed, setTableDialectPrefix) {
		if console.outputFormat != formatter.TableFormat &&
			console.outputFormat != formatter.TTableFormat {
			log.Error("Table dialects supports only for table and ttable output formats")
			return true
		}
		handleTableDialectOption(trimmed, formatterOpts)
		return true
	}

	return false
}
