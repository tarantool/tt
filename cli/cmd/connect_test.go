package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testUser     = "a-фs$d!e%*1#2?3&44"
	testPass     = "bb-фs$d!e%*1#2?3&666"
	testUserPass = testUser + ":" + testPass
)

var validBaseUris = []string{
	"tcp://localhost:11",
	"localhost:123",
	"host:123",
	"123:123",
	"unix://path",
	"unix://path/to/file",
	"unix:///path/to/file",
	"unix://../path/to/file",
	"./a",
	"/1",
}

var validCredentialsUris = []string{
	"tcp://" + testUserPass + "@localhost:11",
	testUserPass + "@localhost:123",
	"unix://" + testUserPass + "@path",
	"unix://" + testUserPass + "@../path/to/file",
	testUserPass + "@./a",
	testUserPass + "@/1",
}

var invalidBaseUris = []string{
	"tcp:localhost:123123",
	"tcp:/anyhost:1",
	"tcp://localhost:asd",
	"tcp:///localhost:11",
	"asd://localhost:111",
	"123://localhost:123",
	"123asd:localhost:222",
	"123",
	"localhost",
	"localhost:asd",
	"unix:",
	"unix:a",
	"unix:/",
	"unix:/a",
	"unix/:",
	"unix/:2",
	"unix//:asd",
	"unix/:/",
	"unix://",
	"unix://.",
	"unix:///",
	".",
	".a",
	"/",
}

var invalidCredentialsUris = []string{
	"tcp://user@localhost:11",
	"user:password@tcp://localhost:11",
	"user@localhost:123",
	"unix://user@path",
	"user:password@unix://path",
	"unix://user@../path/to/file",
	"user:password@unix://../path/to/file",
	"user@./a",
	"user@/1",
}

func TestIsBaseURIValid(t *testing.T) {
	for _, uri := range validBaseUris {
		t.Run(uri, func(t *testing.T) {
			assert.True(t, isBaseURI(uri), "URI must be valid")
		})
	}
}

func TestIsBaseURIInvalid(t *testing.T) {
	invalid := []string{}
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, validCredentialsUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			assert.False(t, isBaseURI(uri), "URI must be invalid")
		})
	}
}

func TestIsCredentialsURIValid(t *testing.T) {
	for _, uri := range validCredentialsUris {
		t.Run(uri, func(t *testing.T) {
			assert.True(t, isCredentialsURI(uri), "URI must be valid")
		})
	}
}

func TestIsCredentialsURIInvalid(t *testing.T) {
	invalid := []string{}
	invalid = append(invalid, validBaseUris...)
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			assert.False(t, isCredentialsURI(uri), "URI must be invalid")
		})
	}
}

func TestParseCredentialsURI(t *testing.T) {
	cases := []struct {
		srcUri string
		newUri string
	}{
		{"tcp://" + testUserPass + "@localhost:3013", "tcp://localhost:3013"},
		{testUserPass + "@localhost:3013", "localhost:3013"},
		{"unix://" + testUserPass + "@/any/path", "unix:///any/path"},
		{testUserPass + "@/path", "/path"},
		{testUserPass + "@./path", "./path"},
	}

	for _, c := range cases {
		t.Run(c.srcUri, func(t *testing.T) {
			newUri, user, pass := parseCredentialsURI(c.srcUri)
			assert.Equal(t, c.newUri, newUri, "a unexpected new URI")
			assert.Equal(t, testUser, user, "a unexpected username")
			assert.Equal(t, testPass, pass, "a unexpected password")
		})
	}
}

func TestParseCredentialsURI_parseValid(t *testing.T) {
	for _, uri := range validCredentialsUris {
		t.Run(uri, func(t *testing.T) {
			newUri, user, pass := parseCredentialsURI(uri)
			assert.NotEqual(t, uri, newUri, "URI must change")
			assert.NotEqual(t, "", user, "username must not be empty")
			assert.NotEqual(t, "", pass, "password must not be empty")
		})
	}
}

func TestParseCredentialsURI_notParseInvalid(t *testing.T) {
	invalid := []string{}
	invalid = append(invalid, validBaseUris...)
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			newUri, user, pass := parseCredentialsURI(uri)
			assert.Equal(t, uri, newUri, "URI must no change")
			assert.Equal(t, "", user, "username must be empty")
			assert.Equal(t, "", pass, "password must be empty")
		})
	}
}
