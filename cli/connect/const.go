package connect

// setLanguagePrefix is used to set a language in the Tarantool console.
const setLanguagePrefix = "\\set language"

// setLanguage is a command to set a current language.
const setLanguage = setLanguagePrefix

// setFormatLong is a command to set an output format.
const setFormatLong = "\\set output"

// setGraphics is a command to switch the pseudo graphics mode.
const setGraphics = "\\set graphics"

// setTableDialect is a command to set a table dialect.
const setTableDialect = "\\set table_format"

// setTableColumnWidthMaxLong is a long command to set a maximum column
// width for tables.
const setTableColumnWidthMaxLong = "\\set table_column_width"

// setDelimiter set a custom expression delimiter for Tarantool console.
const setDelimiter = "\\set delimiter"

// setTableColumnWidthMaxShort is a short command to set a maximum columns
// width for tables.
const setTableColumnWidthMaxShort = "\\xw"

// setNextFormat is a command to set a next format cyclically.
const setNextFormat = "\\x"

// setFormatYaml is a short command to set the YAML format.
const setFormatYaml = "\\xy"

// setFormatLua is a short command to set the Lua format.
const setFormatLua = "\\xl"

// setFormatTable is a short command to set the table format.
const setFormatTable = "\\xt"

// setFormatTable is a short command to set the ttable format.
const setFormatTTable = "\\xT"

// setGraphicsEnable is a command to enable a pseudo graphics output for
// table/ttable output formats.
const setGraphicsEnable = "\\xG"

// setGraphicsDisable is a command to disable a pseudo graphics output for
// table/ttable output formats.
const setGraphicsDisable = "\\xg"

// setQuit is a short command to set ttable format.
var setQuit = []string{"\\quit", "\\q"}

// getShortcutsList is a command to get shortcuts and a list of hotkeys.
const getShortcutsList = "\\shortcuts"

// getHelpCmd is a command to get a help message.
var getHelp = []string{"\\help", "?"}

const shortcutListText = `---
- - |
    Available hotkeys and shortcuts:

       Ctrl + J / Ctrl + M [Enter] -- Enter the command
       Ctrl + A [Home]             -- Go to the beginning of the command
       Ctrl + E [End]              -- Go to the end of the command
       Ctrl + P [Up Arrow]         -- Previous command
       Ctrl + N [Down Arrow]       -- Next command
       Ctrl + F [Right Arrow]      -- Forward one character
       Ctrl + B [Left Arrow]       -- Backward one character
       Ctrl + H [Backspace]        -- Delete character before the cursor
       Ctrl + I [Tab]              -- Get next completion
       BackTab                     -- Get previous completion
       Ctrl + D                    -- Delete character under the cursor
       Ctrl + W                    -- Cut the word before the cursor
       Ctrl + K                    -- Cut the command after the cursor
       Ctrl + U                    -- Cut the command before the cursor
       Ctrl + L                    -- Clear the screen
       Ctrl + R                    -- Enter in the reverse search mode
       Ctrl + C                    -- Interrupt current unfinished expression
       Alt + B                     -- Move backwards one word
       Alt + F                     -- Move forwards one word
...`
