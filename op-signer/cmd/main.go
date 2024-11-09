package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	"github.com/ethereum/go-ethereum/log"

	signer "github.com/ethereum-optimism/infra/op-signer"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
)

var (
	Version   = ""
	GitCommit = ""
	GitDate   = ""
)

func main() {
	oplog.SetupDefaults()

	app := cli.NewApp()
	app.Flags = cliapp.ProtectFlags(signer.CLIFlags("OP_SIGNER"))
	app.Version = fmt.Sprintf("%s-%s-%s", Version, GitCommit, GitDate)
	app.Name = "op-signer"
	app.Usage = "OP Signing Service"
	app.Description = ""
	app.Commands = []*cli.Command{
		{
			Name:  "client",
			Usage: "test client for signer service",
			Subcommands: []*cli.Command{
				{
					Name:   "sign",
					Usage:  "sign a transaction",
					Action: signer.ClientSign(Version),
					Flags:  cliapp.ProtectFlags(signer.ClientSignCLIFlags("SIGNER")),
				},
			},
		},
	}

	app.Action = cliapp.LifecycleCmd(signer.MainAppAction(Version))
	err := app.Run(os.Args)
	if err != nil {
		log.Crit("Application failed", "message", err)
	}
}
