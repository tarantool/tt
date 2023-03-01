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

type userCredentials struct {
	username string
	password string
}

// getCredsInteractive Interactively prompts the user for credentials.
func getCredsInteractive() (userCredentials, error) {
	res := userCredentials{}
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Enter username: ")
	resp, err := reader.ReadString('\n')
	if err != nil {
		return res, err
	}
	res.username = strings.TrimSpace(resp)

	fmt.Printf("Enter password: ")
	bytePass, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return res, err
	}
	res.password = strings.TrimSpace(string(bytePass))
	fmt.Println("")

	return res, nil
}

// getCredsFromFile gets credentials from file.
func getCredsFromFile(path string) (userCredentials, error) {
	res := userCredentials{}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return res, err
	}

	re := regexp.MustCompile("(?P<user>.*):(?P<pass>.*)")
	matches := util.FindNamedMatches(re, strings.TrimSpace(string(data)))

	if len(matches) == 0 {
		return res, fmt.Errorf("corrupted credentials")
	}

	res.username = matches["user"]
	res.password = matches["pass"]

	return res, nil
}

// getCredsFromFile gets credentials from environment variables.
func getCredsFromEnvVars() (userCredentials, error) {
	res := userCredentials{}
	res.username = os.Getenv("TT_EE_USERNAME")
	res.password = os.Getenv("TT_EE_PASSWORD")
	if res.username == "" || res.password == "" {
		return res, fmt.Errorf("no credentials in environment variables were found")
	}
	return res, nil
}

// getCreds gets credentials for tarantool-ee download.
func getCreds(cliOpts *config.CliOpts) (userCredentials, error) {
	if cliOpts.EE == nil || (cliOpts.EE != nil && cliOpts.EE.CredPath == "") {
		creds, err := getCredsFromEnvVars()
		if err == nil {
			return creds, nil
		}
		return getCredsInteractive()
	}

	return getCredsFromFile(cliOpts.EE.CredPath)
}
