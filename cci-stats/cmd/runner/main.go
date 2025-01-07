package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"

	"github.com/axelKingsley/go-circleci"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/config"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/db"
	"github.com/ethereum-optimism/infra/cci-stats/pkg/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cciKey := requiredStr("CCI_KEY")
	dbURI := requiredStr("DATABASE_URI")
	projectSlug := requiredStr("PROJECT_SLUG")
	branchPattern := requiredStr("BRANCH_PATTERN")
	workflowPattern := requiredStr("WORKFLOW_PATTERN")
	fetchLimitDays := requiredInt("FETCH_LIMIT_DAYS")
	maxConcurrentFetchJobs := requiredInt("MAX_CONCURRENT_FETCH_JOBS")
	slowTestThresholdSeconds := requiredInt("SLOW_TEST_THRESHOLD_SECONDS")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConn, err := db.New(ctx, dbURI)
	if err != nil {
		return fmt.Errorf("failed to connect to db: %w", err)
	}
	defer dbConn.Close()

	cfg := config.Config{
		ProjectSlug:              projectSlug,
		BranchPatternRegex:       regexp.MustCompile(branchPattern),
		WorkflowPatternRegex:     regexp.MustCompile(workflowPattern),
		FetchLimitDays:           fetchLimitDays,
		MaxConcurrentFetchJobs:   maxConcurrentFetchJobs,
		SlowTestThresholdSeconds: float64(slowTestThresholdSeconds),
	}

	clientCfg := circleci.DefaultConfig()
	clientCfg.Token = cciKey
	client, err := circleci.NewClient(clientCfg)
	if err != nil {
		return fmt.Errorf("failed to create circleci client: %w", err)
	}

	errC := make(chan error)
	go func() {
		err := service.GenerateReport(ctx, cfg, client, dbConn)
		errC <- err
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)

	for {
		select {
		case <-sigs:
			cancel()
		case err := <-errC:
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func requiredStr(envVar string) string {
	val := os.Getenv(envVar)
	if val == "" {
		panic(fmt.Errorf("%s must be set", envVar))
	}
	return val
}

func requiredInt(envVar string) int {
	val := os.Getenv(envVar)
	if val == "" {
		panic(fmt.Errorf("%s must be set", envVar))
	}
	out, err := strconv.Atoi(val)
	if err != nil {
		panic(fmt.Errorf("%s must be an integer", envVar))
	}
	return out
}
