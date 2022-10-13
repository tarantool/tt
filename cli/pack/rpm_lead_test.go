package pack

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLead(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	lead := genRpmLead("myapp")
	assert.Equal(
		"edabeedb0300000000016d796170700000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000010"+
			"00500000000000000000000000000000000",
		hex(lead),
	)
}
