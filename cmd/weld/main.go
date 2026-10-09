// Command weld is a progressive Go scaffold.
//
// `weld new` creates a minimal Go CLI project and `weld add <capability>`
// extends that same project one capability at a time. The template payloads
// live in the internal/weldtemplate package and are embedded into this
// binary.
package main

import (
	"fmt"
	"os"

	"github.com/Xwudao/weld/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "weld:", err)
		os.Exit(1)
	}
}
