// gohello is the program of the package gohello: an external process of an
// application, which prints a greeting and the arguments it was given.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Printf("hello from go: %s\n", strings.Join(os.Args[1:], " "))
}
