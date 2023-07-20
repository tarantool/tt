package formatter

import (
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// StyleWithoutGraphics defines a style without graphics like below:
// col1  col2  col3       col4
// 1     477   Elizabeth  12
// 3     2804  David      33
// 4     1161  William    81
// 5     1172  Jack       35
// 6     1064  William    25
var StyleWithoutGraphics = table.BoxStyle{
	BottomLeft:       " ",
	BottomRight:      " ",
	BottomSeparator:  " ",
	EmptySeparator:   text.RepeatAndTrim(" ", text.RuneWidthWithoutEscSequences(" ")),
	Left:             " ",
	LeftSeparator:    " ",
	MiddleHorizontal: " ",
	MiddleSeparator:  " ",
	MiddleVertical:   " ",
	PaddingLeft:      " ",
	PaddingRight:     " ",
	PageSeparator:    "\n",
	Right:            " ",
	RightSeparator:   " ",
	TopLeft:          " ",
	TopRight:         " ",
	TopSeparator:     " ",
	UnfinishedRow:    "  ",
}
