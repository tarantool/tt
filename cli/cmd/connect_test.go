package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsURIValid(t *testing.T) {
	uris := []string{
		"localhost:123",
		"tcp:localhost:123123",
		"tcp:/anyhost:1",
		"tcp://localhost:11",
		"asd://localhost:111",
		"123://localhost:123",
		"123asd:localhost:222",
		"host:123",
		"123:123",
		"./a",
		"/1",
		"unix:a",
		"unix:/a",
		"unix/:2",
		"unix://.",
	}

	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			assert.True(t, isURI(uri), "URI must be valid")
		})
	}
}

func TestIsURIInvalid(t *testing.T) {
	uris := []string{
		"123",
		"localhost",
		"localhost:asd",
		"tcp://localhost:asd",
		"tcp:///localhost:11",
		".",
		".a",
		"/",
		"unix:",
		"unix:/",
		"unix/:",
		"unix//:asd",
		"unix/:/",
		"unix://",
		"unix:///",
	}

	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			assert.False(t, isURI(uri), "URI must be invalid")
		})
	}
}
