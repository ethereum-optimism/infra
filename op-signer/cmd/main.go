package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	signer "github.com/ethereum-optimism/infra/op-signer"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/log/logcli"
	clsigner "github.com/ethereum-optimism/optimism/op-service/signer"
)

var (
	Version   = ""
	GitCommit = ""
	GitDate   = ""
)

func main() {
	logcli.SetupDefaults()

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
					Name:   string(signer.SignTransaction),
					Usage:  "sign a transaction, 1 arg: a hex-encoded tx",
					Action: signer.ClientSign(signer.SignTransaction),
					Flags:  cliapp.ProtectFlags(clsigner.CLIFlags("OP_SIGNER", "CLIENT")),
				},
				{
					Name:   string(signer.SignBlockPayload),
					Usage:  "sign a block payload using V1 API, 3 args: payloadHash, chainID, domain",
					Action: signer.ClientSign(signer.SignBlockPayload),
					Flags:  cliapp.ProtectFlags(clsigner.CLIFlags("OP_SIGNER", "CLIENT")),
				},
				{
					Name:   string(signer.SignBlockPayloadV2),
					Usage:  "sign a block payload using V2 API, 3 args: payloadHash, chainID, domain",
					Action: signer.ClientSign(signer.SignBlockPayloadV2),
					Flags:  cliapp.ProtectFlags(clsigner.CLIFlags("OP_SIGNER", "CLIENT")),
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
