package config

import "regexp"

type Config struct {
	ProjectSlug              string
	BranchPatternRegex       *regexp.Regexp
	WorkflowPatternRegex     *regexp.Regexp
	FetchLimitDays           int
	MaxConcurrentFetchJobs   int
	SlowTestThresholdSeconds float64
}
