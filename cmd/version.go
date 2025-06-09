package main

import (
	"fmt"
	"github.com/solopine/txszcopy/build"
	"github.com/urfave/cli/v2"
)

var versionCmd = &cli.Command{
	Name:  "version",
	Usage: "version",
	Action: func(cctx *cli.Context) error {
		version := build.UserVersion()
		fmt.Printf("version: %v\n", version)
		return nil
	},
}
