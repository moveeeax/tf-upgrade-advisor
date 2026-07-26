package main

import (
	"fmt"
	"os"

	"github.com/moveeeax/tf-upgrade-advisor/cmd"
)

func main() {
	code, err := cmd.Run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tf-upgrade-advisor:", err)
	}
	os.Exit(code)
}
