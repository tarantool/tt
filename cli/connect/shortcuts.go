package connect

// getShortcutsList is a command for a get shortcuts and hotkeys list.
const getShortcutsList = "\\shortcuts"

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
