package flags

import (
	"strings"
	"testing"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// TestOptionalFlagsDontSetRequired asserts that all flags deemed optional set
// the Required field to false.
func TestOptionalFlagsDontSetRequired(t *testing.T) {
	for _, flag := range optionalFlags {
		reqFlag, ok := flag.(cli.RequiredFlag)
		require.True(t, ok)
		require.False(t, reqFlag.IsRequired())
	}
}

// TestUniqueFlags asserts that all flag names are unique, to avoid accidental conflicts between the many flags.
func TestUniqueFlags(t *testing.T) {
	seenCLI := make(map[string]struct{})
	for _, flag := range Flags {
		name := flag.Names()[0]
		if _, ok := seenCLI[name]; ok {
			t.Errorf("duplicate flag %s", name)
			continue
		}
		seenCLI[name] = struct{}{}
	}
}

// TestBetaFlags test that all flags starting with "beta." have "BETA_" in the env var, and vice versa.
func TestBetaFlags(t *testing.T) {
	for _, flag := range Flags {
		envFlag, ok := flag.(interface {
			GetEnvVars() []string
		})
		if !ok || len(envFlag.GetEnvVars()) == 0 { // skip flags without env-var support
			continue
		}
		name := flag.Names()[0]
		envName := envFlag.GetEnvVars()[0]
		if strings.HasPrefix(name, "beta.") {
			require.Contains(t, envName, "BETA_", "%q flag must contain BETA in env var to match \"beta.\" flag name", name)
		}
		if strings.Contains(envName, "BETA_") {
			require.True(t, strings.HasPrefix(name, "beta."), "%q flag must start with \"beta.\" in flag name to match \"BETA_\" env var", name)
		}
	}
}

func TestHasEnvVar(t *testing.T) {
	for _, flag := range Flags {
		flagName := flag.Names()[0]

		t.Run(flagName, func(t *testing.T) {
			envFlagGetter, ok := flag.(interface {
				GetEnvVars() []string
			})
			envFlags := envFlagGetter.GetEnvVars()
			require.True(t, ok, "must be able to cast the flag to an EnvVar interface")
			require.Equal(t, 1, len(envFlags), "flags should have exactly one env var")
		})
	}
}

func TestEnvVarFormat(t *testing.T) {
	for _, flag := range Flags {
		flagName := flag.Names()[0]

		t.Run(flagName, func(t *testing.T) {
			envFlagGetter, ok := flag.(interface {
				GetEnvVars() []string
			})
			envFlags := envFlagGetter.GetEnvVars()
			require.True(t, ok, "must be able to cast the flag to an EnvVar interface")
			require.Equal(t, 1, len(envFlags), "flags should have exactly one env var")

			// Special cases for flags that use direct environment variable names
			switch flagName {
			case "orchestrator":
				require.Equal(t, "DEVSTACK_ORCHESTRATOR", envFlags[0])
			case "devnet-env-url":
				require.Equal(t, "DEVNET_ENV_URL", envFlags[0])
			default:
				expectedEnvVar := opservice.FlagNameToEnvVarName(flagName, EnvVarPrefix)
				require.Equal(t, expectedEnvVar, envFlags[0])
			}
		})
	}
}

func TestOrchestratorFeatures(t *testing.T) {
	t.Run("type methods", func(t *testing.T) {
		// Test String method
		assert.Equal(t, "sysgo", OrchestratorSysgo.String())
		assert.Equal(t, "sysext", OrchestratorSysext.String())

		// Test IsValid method
		assert.True(t, OrchestratorSysgo.IsValid())
		assert.True(t, OrchestratorSysext.IsValid())
		assert.False(t, OrchestratorType("invalid").IsValid())
		assert.False(t, OrchestratorType("").IsValid())

		// Test ValidOrchestratorTypes
		types := ValidOrchestratorTypes()
		require.Len(t, types, 2)
		assert.Contains(t, types, OrchestratorSysgo)
		assert.Contains(t, types, OrchestratorSysext)
	})

	t.Run("validation function", func(t *testing.T) {
		validCases := []string{"sysgo", "sysext"}
		for _, valid := range validCases {
			assert.NoError(t, validateOrchestrator(valid))
		}

		invalidCases := []string{"invalid", "", "SYSGO", "SysGo"}
		for _, invalid := range invalidCases {
			err := validateOrchestrator(invalid)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "orchestrator must be one of")
		}
	})

	t.Run("CLI flag validation", func(t *testing.T) {
		app := &cli.App{
			Flags: []cli.Flag{Orchestrator},
			Action: func(ctx *cli.Context) error {
				return nil
			},
		}

		testCases := []struct {
			name        string
			args        []string
			shouldError bool
		}{
			{"valid sysgo", []string{"app", "--orchestrator", "sysgo"}, false},
			{"valid sysext", []string{"app", "--orchestrator", "sysext"}, false},
			{"invalid value", []string{"app", "--orchestrator", "invalid"}, true},
			{"no flag uses default", []string{"app"}, false},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := app.Run(tc.args)
				if tc.shouldError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

func TestDevnetEnvURLFlag(t *testing.T) {
	testCases := []struct {
		name          string
		args          []string
		expectedValue string
	}{
		{"with devnet env url", []string{"app", "--devnet-env-url", "test-devnet.json"}, "test-devnet.json"},
		{"no flag uses default empty", []string{"app"}, ""},
		{"with file path", []string{"app", "--devnet-env-url", "/path/to/devnet.json"}, "/path/to/devnet.json"},
		{"with URL", []string{"app", "--devnet-env-url", "https://example.com/devnet.json"}, "https://example.com/devnet.json"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := &cli.App{
				Flags: []cli.Flag{DevnetEnvURL},
				Action: func(ctx *cli.Context) error {
					value := ctx.String(DevnetEnvURL.Name)
					assert.Equal(t, tc.expectedValue, value)
					return nil
				},
			}

			err := app.Run(tc.args)
			assert.NoError(t, err)
		})
	}
}
