package runner

import "time"

// Test execution constants
const (
	// DefaultTestTimeout is the default timeout for individual tests
	DefaultTestTimeout = 10 * time.Minute

	// Default go binary name
	DefaultGoBinary = "go"

	// Test command arguments
	TestCommand     = "test"
	TestListCommand = "-list"
	JSONFlag        = "-json"
	VerboseFlag     = "-v"
	TimeoutFlag     = "-timeout"
	CountFlag       = "-count"
	RunFlag         = "-run"

	// Test count to disable caching
	DisableCacheCount = "1"

	// Directory patterns
	AllPackagesPattern = "./..."
	CurrentDirPattern  = "."

	// Raw JSON sink type identifier
	RawJSONSinkType = "raw_json"

	// MaxReasonableConcurrency caps auto-determined concurrency to avoid resource exhaustion
	MaxReasonableConcurrency = 32
)
