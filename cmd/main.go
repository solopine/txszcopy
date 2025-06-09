package main

import (
	logging "github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"os"
)

var log = logging.Logger("main")

func SetupLogLevels() {
	if _, set := os.LookupEnv("GOLOG_LOG_LEVEL"); !set {
		_ = logging.SetLogLevel("*", "INFO")
		_ = logging.SetLogLevel("rpc", "ERROR")
	}
}

func main() {
	SetupLogLevels()

	err := os.Setenv("RUST_LOG", "Error")
	if err != nil {
		log.Errorf("err:%+v", err)
		os.Exit(1)
		return
	}

	os.Exit(main1())
}

func main1() int {
	app := &cli.App{
		Name:  "txszcopy",
		Usage: "tx copy for sz",
		Commands: []*cli.Command{
			versionCmd,
			runCmd,
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Errorf("err:%+v", err)
		return 1
	}
	return 0
}
