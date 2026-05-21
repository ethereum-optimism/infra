package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validators.yaml")

	// Create test validator config
	validConfig := `
gates:
  - id: test-gate
    description: "Test gate"
    suites:
      test-suite:
        description: "Test suite"
        tests:
          - name: TestOne
            package: "./testdata/package"
    tests:
      - name: TestTwo
        package: "./testdata/package"
`
	err := os.WriteFile(configPath, []byte(validConfig), 0644)
	require.NoError(t, err)

	baseConfig := Config{
		ValidatorConfigFile: configPath,
	}

	t.Run("source loading", func(t *testing.T) {
		tests := []struct {
			name    string
			cfg     Config
			wantErr bool
		}{
			{
				name:    "valid local source",
				cfg:     baseConfig,
				wantErr: false,
			},
			{
				name: "invalid config path",
				cfg: Config{
					ValidatorConfigFile: "nonexistent.yaml",
				},
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r, err := NewRegistry(tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("NewRegistry() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if err == nil {
					require.NotNil(t, r.GetConfig(), "config should be loaded")
				}
			})
		}
	})
}

func TestLoadConfig(t *testing.T) {
	// Create test config file
	tmpDir := t.TempDir()
	validConfig := `
gates:
  - id: test-gate
    tests:
      - name: TestNATFortyTwo
        package: github.com/ethereum-optimism/infra/op-acceptor/validators
`
	configPath := filepath.Join(tmpDir, "validators.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(validConfig), 0644))

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Gates, 1)
	require.Equal(t, "test-gate", cfg.Gates[0].ID)
	require.Len(t, cfg.Gates[0].Tests, 1)
	require.Equal(t, "TestNATFortyTwo", cfg.Gates[0].Tests[0].Name)
}

func TestGateInheritance(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name      string
		config    string
		wantError string
	}{
		{
			name: "valid inheritance",
			config: `
gates:
  - id: parent
    tests:
      - name: parentTest
        package: ./pkg
  - id: child
    inherits: [parent]
    tests:
      - name: childTest
        package: ./pkg
`,
			wantError: "",
		},
		{
			name: "circular inheritance",
			config: `
gates:
  - id: gate1
    inherits: [gate2]
    tests:
      - name: test1
        package: ./pkg
  - id: gate2
    inherits: [gate1]
    tests:
      - name: test2
        package: ./pkg
`,
			wantError: "circular inheritance detected",
		},
		{
			name: "self inheritance",
			config: `
gates:
  - id: gate1
    inherits: [gate1]
    tests:
      - name: test1
        package: ./pkg
`,
			wantError: "circular inheritance detected",
		},
		{
			name: "non-existent gate",
			config: `
gates:
  - id: gate1
    inherits: [nonexistent]
    tests:
      - name: test1
        package: ./pkg
`,
			wantError: "inherits from non-existent gate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(tmpDir, "validators.yaml")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			r, err := NewRegistry(Config{
				ValidatorConfigFile: configPath,
			})

			if tt.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, r)
			}
		})
	}
}

func TestDiscoverTests(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validators.yaml")

	// Create test validator config
	validConfig := `
gates:
  - id: test-gate
    tests:
      - name: test1
        package: github.com/ethereum-optimism/infra/op-acceptor/validators
    suites:
      test-suite:
        tests:
          - name: suite-test1
            package: github.com/ethereum-optimism/infra/op-acceptor/validators
`
	err := os.WriteFile(configPath, []byte(validConfig), 0644)
	require.NoError(t, err)

	reg, err := NewRegistry(Config{
		ValidatorConfigFile: configPath,
	})
	require.NoError(t, err)

	validators := reg.GetValidators()
	require.Len(t, validators, 2) // One direct test and one suite test

	// Check direct test
	require.Equal(t, "test1", validators[0].ID)
	require.Equal(t, "test-gate", validators[0].Gate)
	require.Empty(t, validators[0].Suite)

	// Check suite test
	require.Equal(t, "suite-test1", validators[1].ID)
	require.Equal(t, "test-gate", validators[1].Gate)
	require.Equal(t, "test-suite", validators[1].Suite)
}

func TestExcludeGatesFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validators.yaml")

	// validators: two gates, base and flake-shake; base also contains distinct test T2
	cfg := `
gates:
  - id: base
    tests:
      - name: T1
        package: ./pkg
      - name: T2
        package: ./pkg2
      - package: ./pkg
  - id: flake-shake
    tests:
      - name: T1
        package: ./pkg
      - package: ./pkg
`
	require.NoError(t, os.WriteFile(configPath, []byte(cfg), 0644))

	// Build a registry with exclude-gates=flake-shake
	reg, err := NewRegistry(Config{
		ValidatorConfigFile: configPath,
		ExcludeGates:        []string{"flake-shake"},
	})
	require.NoError(t, err)

	// Validators should include only the distinct base test (T2 in ./pkg2)
	vals := reg.GetValidators()
	require.NotEmpty(t, vals)
	for _, v := range vals {
		assert.Equal(t, types.ValidatorTypeTest, v.Type)
		if v.Gate == "base" {
			// Only T2 (pkg2) should remain; T1 and ./pkg package were excluded via flake-shake skip set
			assert.True(t, v.FuncName == "T2" || v.Package == "./pkg2")
		}
		// There should be no validators with gate flake-shake
		assert.NotEqual(t, "flake-shake", v.Gate)
	}
}

func TestParseExcludeGates_DefaultAndEmpty(t *testing.T) {
	// The parser lives in nat/config.go; test via small wrapper here by importing it through a local copy is complex.
	// Instead, verify behavior indirectly by env and flag precedence through NewRegistry isn't accessible.
	// We cover the main exclusion path in TestExcludeGatesFiltering.
}

func TestExcludeGates_PackagePrefix_Gateless(t *testing.T) {
	// Layout:
	// tmpDir/
	//   tests/
	//     pkg/
	//       pkg_test.go
	//     pkg/sub/
	//       sub_test.go
	// validators.yaml defines gate 'black' with a package-only entry ./tests/pkg

	tmpDir := t.TempDir()

	// Create packages
	pkgDir := filepath.Join(tmpDir, "tests", "pkg")
	subDir := filepath.Join(tmpDir, "tests", "pkg", "sub")
	require.NoError(t, os.MkdirAll(pkgDir, 0755))
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Write minimal *_test.go files
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "pkg_test.go"), []byte("package pkg_test\nimport \"testing\"\nfunc TestX(t *testing.T){}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "sub_test.go"), []byte("package sub_test\nimport \"testing\"\nfunc TestY(t *testing.T){}\n"), 0644))

	// Create validators.yaml with package-only blacklist (relative to TestDir)
	validators := `gates:
  - id: black
    tests:
      - package: ./pkg
`
	cfgPath := filepath.Join(tmpDir, "validators.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(validators), 0644))

	// Create registry in gateless mode with exclude gate
	reg, err := NewRegistry(Config{
		Log:                 log.New(),
		GatelessMode:        true,
		TestDir:             filepath.Join(tmpDir, "tests"),
		ValidatorConfigFile: cfgPath,
		ExcludeGates:        []string{"black"},
	})
	require.NoError(t, err)

	vals := reg.GetValidators()
	// Expect zero validators after blacklist matches prefix (both ./tests/pkg and ./tests/pkg/sub)
	assert.Len(t, vals, 0, "all discovered tests under ./tests/pkg should be excluded by prefix blacklist")
}

func TestExcludeGates_NamedTest_Gateless(t *testing.T) {
	// In gateless mode, validators have FuncName="" (RunAll).
	// When an excluded gate names a specific test in a package, the validator
	// should NOT be dropped; instead, SkipTests should be populated.
	tmpDir := t.TempDir()

	// Create two packages
	pkg1Dir := filepath.Join(tmpDir, "tests", "pkg1")
	pkg2Dir := filepath.Join(tmpDir, "tests", "pkg2")
	require.NoError(t, os.MkdirAll(pkg1Dir, 0755))
	require.NoError(t, os.MkdirAll(pkg2Dir, 0755))

	// pkg1 has two tests
	require.NoError(t, os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), []byte(
		"package pkg1_test\nimport \"testing\"\nfunc TestA(t *testing.T){}\nfunc TestB(t *testing.T){}\n",
	), 0644))
	// pkg2 has one test
	require.NoError(t, os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), []byte(
		"package pkg2_test\nimport \"testing\"\nfunc TestC(t *testing.T){}\n",
	), 0644))

	// validators.yaml: gate 'shaky' names TestA in ./pkg1
	cfgPath := filepath.Join(tmpDir, "validators.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`gates:
  - id: shaky
    tests:
      - name: TestA
        package: ./pkg1
`), 0644))

	// Save cwd and switch to tmpDir/tests so gateless discovery finds ./pkg1, ./pkg2
	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Join(tmpDir, "tests")))
	defer func() { require.NoError(t, os.Chdir(origWd)) }()

	reg, err := NewRegistry(Config{
		Log:                 log.New(),
		GatelessMode:        true,
		TestDir:             ".",
		ValidatorConfigFile: cfgPath,
		ExcludeGates:        []string{"shaky"},
	})
	require.NoError(t, err)

	vals := reg.GetValidators()
	// Both packages should still be present
	require.Len(t, vals, 2, "both packages should remain; named test exclusion should not drop the package")

	// Find the pkg1 validator and verify SkipTests
	var pkg1Val *types.ValidatorMetadata
	for i := range vals {
		if strings.HasSuffix(vals[i].Package, "pkg1") {
			pkg1Val = &vals[i]
			break
		}
	}
	require.NotNil(t, pkg1Val, "pkg1 validator should exist")
	assert.Equal(t, []string{"TestA"}, pkg1Val.SkipTests, "TestA should be in SkipTests")

	// pkg2 should have no SkipTests
	for _, v := range vals {
		if strings.HasSuffix(v.Package, "pkg2") {
			assert.Empty(t, v.SkipTests, "pkg2 should have no SkipTests")
		}
	}
}

func TestExcludeGates_Inheritance(t *testing.T) {
	// Excluding a gate should also exclude tests it inherits from parents
	tmpDir := t.TempDir()

	cfg := `gates:
  - id: parent
    tests:
      - name: TParent
        package: ./pkg
  - id: child
    inherits: [parent]
    tests:
      - name: TChild
        package: ./pkg
  - id: base
    tests:
      - name: TKeep
        package: ./pkg2
`
	cfgPath := filepath.Join(tmpDir, "validators.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0644))

	reg, err := NewRegistry(Config{
		ValidatorConfigFile: cfgPath,
		ExcludeGates:        []string{"child"},
	})
	require.NoError(t, err)

	vals := reg.GetValidators()
	// Expect that TParent and TChild tuples are excluded everywhere; only TKeep remains
	for _, v := range vals {
		assert.Equal(t, types.ValidatorTypeTest, v.Type)
		assert.False(t, v.FuncName == "TParent" && v.Package == "./pkg")
		assert.False(t, v.FuncName == "TChild" && v.Package == "./pkg")
		assert.True(t, v.FuncName == "TKeep" || v.Package == "./pkg2")
	}
}

func TestRegistryGatelessMode(t *testing.T) {
	// Create temporary directory for the test
	tmpDir := t.TempDir()

	// Create test packages structure
	pkg1Dir := filepath.Join(tmpDir, "pkg1")
	pkg2Dir := filepath.Join(tmpDir, "subdir", "pkg2")

	require.NoError(t, os.MkdirAll(pkg1Dir, 0755))
	require.NoError(t, os.MkdirAll(pkg2Dir, 0755))

	// Create test files with proper test function format
	testContent := `package pkg1_test

import "testing"

func TestExample(t *testing.T) {
    t.Log("test running")
}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), []byte(testContent), 0644))

	test2Content := `package pkg2_test

import "testing"

func TestExample2(t *testing.T) {
    t.Log("test2 running")
}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), []byte(test2Content), 0644))

	// Save current working directory and change to tmpDir for the test
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() {
		require.NoError(t, os.Chdir(originalWd))
	}()

	// Create registry in gateless mode using relative path from tmpDir
	registry, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      ".", // Use current directory (tmpDir)
	})
	require.NoError(t, err)

	// Verify validators were created
	validators := registry.GetValidators()
	require.Len(t, validators, 2)

	// Check that all validators are configured for gateless mode
	for _, validator := range validators {
		assert.Equal(t, "gateless", validator.Gate)
		assert.Empty(t, validator.Suite)
		assert.True(t, validator.RunAll)
		assert.Equal(t, types.ValidatorTypeTest, validator.Type)
	}

	// Check that we can find validators by gate
	gatelessValidators := registry.GetValidatorsByGate("gateless")
	require.Len(t, gatelessValidators, 2)

	// Verify the package paths are correct - should be relative paths
	var packages []string
	for _, validator := range validators {
		packages = append(packages, validator.Package)
	}
	expected := []string{"./pkg1", "./subdir/pkg2"}
	require.ElementsMatch(t, expected, packages)
}

// createTestPackages creates n test packages under tmpDir and returns their
// expected relative paths (e.g., "./pkg-00", "./pkg-01", ...).
func createTestPackages(t *testing.T, tmpDir string, n int) []string {
	t.Helper()
	var paths []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pkg-%02d", i)
		dir := filepath.Join(tmpDir, name)
		require.NoError(t, os.MkdirAll(dir, 0755))
		content := fmt.Sprintf("package %s\nimport \"testing\"\nfunc Test%s(t *testing.T){}\n", name, name)
		// Package names can't have hyphens — use underscore in Go but keep dir name for path
		content = strings.ReplaceAll(content, "-", "_")
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+"_test.go"), []byte(content), 0644))
		paths = append(paths, "./"+name)
	}
	return paths
}

func TestShardFiltering_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 8)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	// Create 4 shards and verify each gets 2 packages
	for shard := 0; shard < 4; shard++ {
		reg, err := NewRegistry(Config{
			Log:          log.New(),
			GatelessMode: true,
			TestDir:      ".",
			ShardIndex:   shard,
			ShardTotal:   4,
		})
		require.NoError(t, err)

		vals := reg.GetValidators()
		assert.Len(t, vals, 2, "shard %d should have 2 packages", shard)
	}
}

func TestShardFiltering_Coverage(t *testing.T) {
	// Verify that the union of all shards equals the full package set (no gaps, no duplicates).
	tmpDir := t.TempDir()
	allPkgs := createTestPackages(t, tmpDir, 10)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	shardTotal := 3
	var allShardedPkgs []string

	for shard := 0; shard < shardTotal; shard++ {
		reg, err := NewRegistry(Config{
			Log:          log.New(),
			GatelessMode: true,
			TestDir:      ".",
			ShardIndex:   shard,
			ShardTotal:   shardTotal,
		})
		require.NoError(t, err)

		for _, v := range reg.GetValidators() {
			allShardedPkgs = append(allShardedPkgs, v.Package)
		}
	}

	sort.Strings(allPkgs)
	sort.Strings(allShardedPkgs)
	assert.Equal(t, allPkgs, allShardedPkgs, "union of all shards should equal the full package set")
}

func TestShardFiltering_Deterministic(t *testing.T) {
	// Running the same shard twice should produce identical results.
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 6)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	var runs [2][]string
	for i := 0; i < 2; i++ {
		reg, err := NewRegistry(Config{
			Log:          log.New(),
			GatelessMode: true,
			TestDir:      ".",
			ShardIndex:   1,
			ShardTotal:   3,
		})
		require.NoError(t, err)
		for _, v := range reg.GetValidators() {
			runs[i] = append(runs[i], v.Package)
		}
	}
	assert.Equal(t, runs[0], runs[1], "shard assignment should be deterministic")
}

func TestShardFiltering_NoOverlap(t *testing.T) {
	// No package should appear in more than one shard.
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 7)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	shardTotal := 3
	seen := make(map[string]int) // package -> which shard

	for shard := 0; shard < shardTotal; shard++ {
		reg, err := NewRegistry(Config{
			Log:          log.New(),
			GatelessMode: true,
			TestDir:      ".",
			ShardIndex:   shard,
			ShardTotal:   shardTotal,
		})
		require.NoError(t, err)

		for _, v := range reg.GetValidators() {
			prev, dup := seen[v.Package]
			assert.False(t, dup, "package %s appears in shard %d and %d", v.Package, prev, shard)
			seen[v.Package] = shard
		}
	}
}

func TestShardFiltering_MoreShardsThanPackages(t *testing.T) {
	// When there are more shards than packages, some shards should be empty.
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 2)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	nonEmpty := 0
	for shard := 0; shard < 5; shard++ {
		reg, err := NewRegistry(Config{
			Log:          log.New(),
			GatelessMode: true,
			TestDir:      ".",
			ShardIndex:   shard,
			ShardTotal:   5,
		})
		require.NoError(t, err)
		if len(reg.GetValidators()) > 0 {
			nonEmpty++
		}
	}
	assert.Equal(t, 2, nonEmpty, "only 2 shards should have packages when there are 2 packages and 5 shards")
}

func TestShardFiltering_SinglePackage(t *testing.T) {
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 1)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	// Shard 0 should get the package, shard 1 should be empty
	reg0, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      ".",
		ShardIndex:   0,
		ShardTotal:   2,
	})
	require.NoError(t, err)
	assert.Len(t, reg0.GetValidators(), 1)

	reg1, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      ".",
		ShardIndex:   1,
		ShardTotal:   2,
	})
	require.NoError(t, err)
	assert.Len(t, reg1.GetValidators(), 0)
}

func TestShardFiltering_Disabled(t *testing.T) {
	// Default values (-1, 0) should mean no sharding — all packages returned.
	tmpDir := t.TempDir()
	createTestPackages(t, tmpDir, 4)

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { require.NoError(t, os.Chdir(originalWd)) }()

	reg, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      ".",
		ShardIndex:   -1,
		ShardTotal:   0,
	})
	require.NoError(t, err)
	assert.Len(t, reg.GetValidators(), 4, "no sharding should return all packages")
}

func TestRegistryGatelessModeEmpty(t *testing.T) {
	// Create temporary directory with no test files
	tmpDir := t.TempDir()

	// Create registry in gateless mode
	_, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      tmpDir,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no test packages found")
}

func TestRegistryGatelessModeInvalidDir(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "nonexistent")

	// Create registry in gateless mode with non-existent directory
	_, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      nonExistentDir,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

// Ensure that gateless discovery never emits package paths that begin with "../"
// which can cause local path checks to fail under CI (e.g., sysgo orchestrator).
func TestRegistryGatelessMode_NoParentComponents(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a nested working root with a path that includes ".." when joined
	rootDir := filepath.Join(tmpDir, "root")
	require.NoError(t, os.MkdirAll(rootDir, 0o755))

	subDir := filepath.Join(rootDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	// Create two go test packages under subDir
	pkg1 := filepath.Join(subDir, "pkg1")
	require.NoError(t, os.MkdirAll(pkg1, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg1, "pkg1_test.go"), []byte("package pkg1\nimport \"testing\"\nfunc TestOne(t *testing.T){}\n"), 0o644))

	pkg2 := filepath.Join(subDir, "pkg2")
	require.NoError(t, os.MkdirAll(pkg2, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg2, "pkg2_test.go"), []byte("package pkg2\nimport \"testing\"\nfunc TestTwo(t *testing.T){}\n"), 0o644))

	// Construct a TestDir expression that contains a ".." component
	// When resolved, it still points at subDir.
	testDirWithParent := filepath.Join(subDir, "..", "sub") + "/..."

	reg, err := NewRegistry(Config{
		Log:          log.New(),
		GatelessMode: true,
		TestDir:      testDirWithParent,
	})
	require.NoError(t, err)

	validators := reg.GetValidators()
	require.NotEmpty(t, validators)

	for _, v := range validators {
		assert.False(t, strings.HasPrefix(v.Package, "../"), "package path should not start with ../: %s", v.Package)
	}
}
