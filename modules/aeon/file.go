package aeon

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"strings"
)

// isURL reports whether str is a URL with a scheme and a host and without
// user information, or a unix: address.
func isURL(str string) bool {
	if strings.HasPrefix(str, "unix:") {
		return true
	}

	u, err := url.Parse(str)

	return err == nil && u.Scheme != "" && u.Host != "" && u.Opaque == "" && u.User == nil
}

// removeScheme returns inputURL without its scheme; a unix URL is returned
// as it is.
func removeScheme(inputURL string) (string, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", fmt.Errorf("cannot remove the scheme: %w", err)
	}

	if parsedURL.Scheme == "unix" {
		return inputURL, nil
	}

	parsedURL.Scheme = ""

	return strings.Replace(parsedURL.String(), "//", "", 1), nil
}

// isRegularFile reports whether filePath names an existing regular file.
func isRegularFile(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.Mode().IsRegular()
}

// homeDir returns the home directory of the current user: $HOME, or the one
// the user database has when HOME is not set. A process may run as a user
// the database does not know, as a container started with an arbitrary uid
// does, and has a HOME all the same.
func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil {
		return home, nil
	}

	usr, userErr := user.Current()
	if userErr != nil {
		return "", fmt.Errorf("cannot get the home directory: %w; %w", err, userErr)
	}

	return usr.HomeDir, nil
}
