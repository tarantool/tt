package install_ee

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/term"
)

type UserCredentials struct {
	Username string
	Password string
}

// getCredsInteractive Interactively prompts the user for credentials.
func getCredsInteractive() (UserCredentials, error) {
	res := UserCredentials{}
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Enter Username: ")
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
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return res, err
	}

	re := regexp.MustCompile("(?P<user>.*):(?P<pass>.*)")
	matches := util.FindNamedMatches(re, strings.TrimSpace(string(data)))

	if len(matches) == 0 {
		return res, fmt.Errorf("corrupted credentials")
	}

	res.Username = matches["user"]
	res.Password = matches["pass"]

	return res, nil
}

// getCredsFromFile gets credentials from environment variables.
func getCredsFromEnvVars() (UserCredentials, error) {
	res := UserCredentials{}
	res.Username = os.Getenv("TT_CLI_EE_USERNAME")
	res.Password = os.Getenv("TT_CLI_EE_PASSWORD")
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
