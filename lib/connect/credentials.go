package connect

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/tarantool/tt/cli/config"
	"golang.org/x/term"
)

const (
	EnvSdkUsername = "TT_CLI_EE_USERNAME"
	EnvSdkPassword = "TT_CLI_EE_PASSWORD"
)

type UserCredentials struct {
	Username string
	Password string
}

// getCredsInteractive Interactively prompts the user for credentials.
func getCredsInteractive() (UserCredentials, error) {
	res := UserCredentials{}
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Signing in to Customer zone.")
	fmt.Printf("Enter Email: ")
	resp, err := reader.ReadString('\n')
	if err != nil {
		return res, err
	}
	res.Username = strings.TrimSpace(resp)

	fmt.Printf("Enter Password: ")
	bytePass, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return res, err
	}
	res.Password = strings.TrimSpace(string(bytePass))
	fmt.Println("")

	return res, nil
}

// getCredsFromFile gets credentials from file.
func getCredsFromFile(path string) (UserCredentials, error) {
	res := UserCredentials{}

	fh, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer fh.Close()

	info, err := fh.Stat()
	if err != nil {
		return res, err
	}

	// Check file permissions. Error if `group` or `other` bits are set.
	if info.Mode().Perm()&os.FileMode(0o077) != 0 {
		return res, fmt.Errorf("permissions %q for %q are too open.\n\t%s\n\t%s %s'",
			info.Mode(),
			path,
			"It is required that the credential file is NOT accessible by others.",
			"Can be fixed by running: 'chmod 0600",
			path,
		)
	}

	scanner := bufio.NewScanner(fh)
	scanner.Scan()
	res.Username = scanner.Text()
	scanner.Scan()
	res.Password = scanner.Text()

	if scanner.Err() != nil {
		return res, scanner.Err()
	}

	if len(res.Username) == 0 {
		return res, fmt.Errorf("login not set")
	}
	if len(res.Password) == 0 {
		return res, fmt.Errorf("password not set")
	}

	return res, nil
}

// getCredsFromFile gets credentials from environment variables.
func getCredsFromEnvVars() (UserCredentials, error) {
	res := UserCredentials{}
	res.Username = os.Getenv(EnvSdkUsername)
	res.Password = os.Getenv(EnvSdkPassword)
	if res.Username == "" || res.Password == "" {
		return res, fmt.Errorf("no credentials in environment variables were found")
	}
	return res, nil
}

// getCreds gets credentials for tarantool-ee download.
func GetCreds(cliOpts *config.CliOpts) (UserCredentials, error) {
	if cliOpts.EE == nil || (cliOpts.EE != nil && cliOpts.EE.CredPath == "") {
		creds, err := getCredsFromEnvVars()
		if err == nil {
			return creds, nil
		}
		return getCredsInteractive()
	}

	return getCredsFromFile(cliOpts.EE.CredPath)
}
