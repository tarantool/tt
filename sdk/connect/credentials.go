package connect

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

var (
	errLoginNotSet                                  = errors.New("login not set")
	errNoCredentialsInEnvironmentVariablesWereFound = errors.New(
		"no credentials in environment variables were found",
	)
	errPasswordNotSet           = errors.New("password not set")
	errPermissionsForAreTooOpen = errors.New("permissions ")
)

const forbiddenCredentialPermissions = 0o077

const (
	EnvSdkUsername = "TT_CLI_EE_USERNAME"
	EnvSdkPassword = "TT_CLI_EE_PASSWORD" //nolint:gosec // The name of a variable, not a secret.
)

type UserCredentials struct {
	Username string
	Password string
}

// ErrNoCredentials reports customer-zone credentials that are needed and
// given nowhere, where no prompt can ask for them: stdin is not a terminal.
var ErrNoCredentials = errors.New("customer-zone credentials are needed for " +
	"Tarantool Enterprise and are not given: " +
	"pass --credentials-file FILE, the login and the password on two lines, or set " +
	EnvSdkUsername + " and " + EnvSdkPassword)

// stdinIsTerminal reports whether a prompt can be answered. A variable for
// the tests.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(syscall.Stdin)
}

// getCredsInteractive Interactively prompts the user for credentials. The
// prompts go to stderr, so stdout keeps only the result of the command.
func getCredsInteractive() (UserCredentials, error) {
	res := UserCredentials{Username: "", Password: ""}

	if !stdinIsTerminal() {
		return res, ErrNoCredentials
	}

	reader := bufio.NewReader(os.Stdin)

	_, _ = fmt.Fprintln(os.Stderr, "Signing in to Customer zone.")
	_, _ = fmt.Fprintf(os.Stderr, "Enter Email: ")

	resp, err := reader.ReadString('\n')
	if err != nil {
		return res, fmt.Errorf("reading the email: %w", err)
	}

	res.Username = strings.TrimSpace(resp)

	_, _ = fmt.Fprintf(os.Stderr, "Enter Password: ")

	bytePass, err := term.ReadPassword(syscall.Stdin)
	if err != nil {
		return res, fmt.Errorf("reading the password: %w", err)
	}

	res.Password = strings.TrimSpace(string(bytePass))
	_, _ = fmt.Fprintln(os.Stderr, "")

	return res, nil
}

// getCredsFromFile gets credentials from file.
func getCredsFromFile(path string) (UserCredentials, error) {
	res := UserCredentials{Username: "", Password: ""}

	file, err := os.Open(path)
	if err != nil {
		return res, fmt.Errorf("opening the credentials file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return res, fmt.Errorf("checking the credentials file: %w", err)
	}

	// Check file permissions. Error if `group` or `other` bits are set.
	if info.Mode().Perm()&os.FileMode(forbiddenCredentialPermissions) != 0 {
		return res, fmt.Errorf("%w%q for %q are too open.\n\t%s\n\t%s %s'",
			errPermissionsForAreTooOpen, info.Mode(),
			path,
			"It is required that the credential file is NOT accessible by others.",
			"Can be fixed by running: 'chmod 0600",
			path,
		)
	}

	scanner := bufio.NewScanner(file)
	scanner.Scan()

	res.Username = scanner.Text()
	scanner.Scan()

	res.Password = scanner.Text()

	err = scanner.Err()
	if err != nil {
		return res, fmt.Errorf("reading the credentials file %q: %w", path, err)
	}

	if len(res.Username) == 0 {
		return res, errLoginNotSet
	}

	if len(res.Password) == 0 {
		return res, errPasswordNotSet
	}

	return res, nil
}

// getCredsFromFile gets credentials from environment variables.
func getCredsFromEnvVars() (UserCredentials, error) {
	res := UserCredentials{Username: "", Password: ""}

	res.Username = os.Getenv(EnvSdkUsername)
	res.Password = os.Getenv(EnvSdkPassword)

	switch {
	case res.Username == "" && res.Password == "":
		return res, errNoCredentialsInEnvironmentVariablesWereFound
	case res.Username == "":
		return res, fmt.Errorf("%w: %s is set, %s is not",
			errLoginNotSet, EnvSdkPassword, EnvSdkUsername)
	case res.Password == "":
		return res, fmt.Errorf("%w: %s is set, %s is not",
			errPasswordNotSet, EnvSdkUsername, EnvSdkPassword)
	}

	return res, nil
}

// GetCreds gets credentials for tarantool-ee download: from the file at
// credPath, from TT_CLI_EE_USERNAME and TT_CLI_EE_PASSWORD, or from a prompt
// on a terminal. Off a terminal, credentials given nowhere are
// ErrNoCredentials, and only one of the variables set is an error naming
// the other.
func GetCreds(credPath string) (UserCredentials, error) {
	if credPath == "" {
		creds, err := getCredsFromEnvVars()
		if err == nil {
			return creds, nil
		}

		if !errors.Is(err, errNoCredentialsInEnvironmentVariablesWereFound) {
			return creds, err
		}

		return getCredsInteractive()
	}

	return getCredsFromFile(credPath)
}
