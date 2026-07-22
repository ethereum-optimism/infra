package main

import (
	"bailiff/internal/pkg/bailiff"
	"bailiff/internal/pkg/version"
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	"github.com/ethereum-optimism/optimism/op-service/cliapp"
	"github.com/ethereum-optimism/optimism/op-service/ctxinterrupt"
	"github.com/ethereum-optimism/optimism/op-service/httputil"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/google/go-github/v66/github"
	"github.com/urfave/cli/v2"
)

var (
	GitCommit = ""
	GitDate   = ""
)

// VersionWithMeta holds the textual version string including the metadata.
var VersionWithMeta = opservice.FormatVersion(version.Version, GitCommit, GitDate, version.Meta)

const (
	EnvVarPrefix = "BAILIFF"

	ConfigPathFlagName     = "config-path"
	WebhookSecretFlagName  = "webhook-secret"
	GithubTokenFlagName    = "github-token"
	PrivateKeyFileFlagName = "private-key-file"
)

var (
	ConfigPathFlag = &cli.StringFlag{
		Name:     ConfigPathFlagName,
		Usage:    "Path to the configuration file",
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "CONFIG_PATH"),
	}
	WebhookSecretFlag = &cli.StringFlag{
		Name:     WebhookSecretFlagName,
		Usage:    "Secret used to validate incoming webhooks",
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "WEBHOOK_SECRET"),
	}
	GithubTokenFlag = &cli.StringFlag{
		Name:     GithubTokenFlagName,
		Usage:    "GitHub token used to interact with the GitHub API",
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "GITHUB_TOKEN"),
	}
	PrivateKeyFileFlag = &cli.StringFlag{
		Name:     PrivateKeyFileFlagName,
		Usage:    "Path to the private key file",
		Required: true,
		EnvVars:  opservice.PrefixEnvVar(EnvVarPrefix, "PRIVATE_KEY_FILE"),
	}
)

var GlobalFlags = []cli.Flag{
	ConfigPathFlag,
	WebhookSecretFlag,
	GithubTokenFlag,
	PrivateKeyFileFlag,
}

func main() {
	app := cli.NewApp()
	app.Version = VersionWithMeta
	app.Name = "bailiff"
	app.Usage = "Tool to authorize pull requests from external contributors."
	app.Flags = cliapp.ProtectFlags(GlobalFlags)
	app.Action = func(cliCtx *cli.Context) error {
		logCfg := oplog.ReadCLIConfig(cliCtx)
		l := oplog.NewLogger(oplog.AppOut(cliCtx), logCfg)
		oplog.SetGlobalLogHandler(l.Handler())

		cfgPath := cliCtx.String(ConfigPathFlagName)
		cfg, err := bailiff.ReadConfig(cfgPath)
		if err != nil {
			return err
		}
		if err := cfg.Check(); err != nil {
			return fmt.Errorf("invalid configuration file: %w", err)
		}

		webhookSecret := cliCtx.String(WebhookSecretFlagName)
		githubToken := cliCtx.String(GithubTokenFlagName)
		privateKeyFile := cliCtx.String(PrivateKeyFileFlagName)
		envCfg := bailiff.EnvConfig{
			WebhookSecret:  webhookSecret,
			GitHubToken:    githubToken,
			PrivateKeyFile: privateKeyFile,
		}
		if err := envCfg.Check(); err != nil {
			return fmt.Errorf("invalid environment configuration: %w", err)
		}

		workdir, err := os.MkdirTemp("", "bailiff-")
		if err != nil {
			return fmt.Errorf("failed to create workdir: %w", err)
		}

		gh := github.NewClient(http.DefaultClient).WithAuthToken(envCfg.GitHubToken)
		wl := bailiff.NewTeamWhitelist(cfg.Org, cfg.AdminTeams, gh)

		repusher := bailiff.NewShellRepusher(l.New("module", "shell-repusher"), workdir, envCfg.PrivateKeyFile)
		asyncRepusher := bailiff.NewAsyncRepusher(l.New("module", "async-repusher"), repusher)
		eh := bailiff.NewEventHandler(gh, wl, cfg, workdir, l, asyncRepusher)
		srv := bailiff.NewServer(l, envCfg.WebhookSecret, eh)

		repoURL := fmt.Sprintf("git@github.com:%s/%s.git", cfg.Org, cfg.Repo)
		if err := repusher.Clone(cliCtx.Context, repoURL); err != nil {
			return fmt.Errorf("failed to clone repo: %w", err)
		}

		ctx, cancel := context.WithCancel(cliCtx.Context)
		defer cancel()

		go wl.SyncPeriodically(ctx, l, time.Minute)

		metricsCfg := opmetrics.ReadCLIConfig(cliCtx)
		if metricsCfg.Enabled {
			metricsSrv, err := opmetrics.StartServer(bailiff.MetricsRegistry, metricsCfg.ListenAddr, metricsCfg.ListenPort)
			if err != nil {
				return fmt.Errorf("failed to start metrics server: %w", err)
			}
			defer func() {
				if err := metricsSrv.Stop(context.Background()); err != nil {
					l.Error("failed to stop metrics server", "err", err)
				}
			}()

			l.Info("metrics server is running", "addr", metricsCfg.ListenAddr)
		}

		httpSrv, err := httputil.StartHTTPServer(cfg.ListenAddr, srv)
		if err != nil {
			return fmt.Errorf("failed to start HTTP server: %w", err)
		}

		defer func() {
			if err := httpSrv.Stop(context.Background()); err != nil {
				l.Error("failed to stop DA server", "err", err)
			}
		}()

		l.Info("bailiff is running", "addr", cfg.ListenAddr)
		return ctxinterrupt.Wait(ctx)
	}
	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Application failed: %v\n", err)
	}
}

func init() {
	GlobalFlags = append(GlobalFlags, oplog.CLIFlags(EnvVarPrefix)...)
	GlobalFlags = append(GlobalFlags, opmetrics.CLIFlags(EnvVarPrefix)...)
}
