package types

import "time"

// EffectiveConfigSnapshot represents the effective runtime configuration grouped by domain.
type EffectiveConfigSnapshot struct {
	Runner        RunnerConfigSnapshot        `json:"runner"`
	Orchestration OrchestrationConfigSnapshot `json:"orchestration"`
	Logging       LoggingConfigSnapshot       `json:"logging"`
	Execution     ExecutionConfigSnapshot     `json:"execution"`
	Paths         PathsConfigSnapshot         `json:"paths"`

	// Metadata
	NetworkName string `json:"networkName"`
	RunID       string `json:"runId,omitempty"`
}

type RunnerConfigSnapshot struct {
	AllowSkips       bool          `json:"allowSkips"`
	DefaultTimeout   time.Duration `json:"defaultTimeout"`
	Timeout          time.Duration `json:"timeout"`
	Serial           bool          `json:"serial"`
	Concurrency      int           `json:"concurrency"`
	ShowProgress     bool          `json:"showProgress"`
	ProgressInterval time.Duration `json:"progressInterval"`
}

type OrchestrationConfigSnapshot struct {
	Orchestrator string `json:"orchestrator"`
	DevnetEnvURL string `json:"devnetEnvURL,omitempty"`
}

type LoggingConfigSnapshot struct {
	TestLogLevel       string `json:"testLogLevel"`
	OutputRealtimeLogs bool   `json:"outputRealtimeLogs"`
}

type ExecutionConfigSnapshot struct {
	RunInterval time.Duration `json:"runInterval"`
	RunOnce     bool          `json:"runOnce"`
	GoBinary    string        `json:"goBinary"`
	TargetGate  string        `json:"targetGate"`
	Gateless    bool          `json:"gatelessMode"`
}

type PathsConfigSnapshot struct {
	TestDir         string `json:"testDir"`
	ValidatorConfig string `json:"validatorConfig"`
	LogDir          string `json:"logDir"`
	WorkDir         string `json:"workDir"`
}
