package testlist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindTestFunctions(t *testing.T) {
	tests := []struct {
		name     string
		pkgPath  string
		setup    func(string) error
		expected []string
	}{
		{
			name:    "module path",
			pkgPath: "github.com/test/module/pkg",
			setup: func(dir string) error {
				// Create go.mod
				goModContent := "module github.com/test/module\n\ngo 1.21\n"
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModContent), 0644); err != nil {
					return err
				}

				// Create package directory
				pkgDir := filepath.Join(dir, "pkg")
				if err := os.MkdirAll(pkgDir, 0755); err != nil {
					return err
				}

				// Create test files
				return createTestFiles(pkgDir)
			},
			expected: []string{
				"TestNormal",
				"TestAnother",
				"TestWithMain",
				"TestWithBenchmark",
			},
		},
		{
			name:    "relative path",
			pkgPath: "./pkg",
			setup: func(dir string) error {
				// Create package directory
				pkgDir := filepath.Join(dir, "pkg")
				if err := os.MkdirAll(pkgDir, 0755); err != nil {
					return err
				}

				// Create test files
				return createTestFiles(pkgDir)
			},
			expected: []string{
				"TestNormal",
				"TestAnother",
				"TestWithMain",
				"TestWithBenchmark",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for the test
			tmpDir := t.TempDir()

			// Run setup
			err := tt.setup(tmpDir)
			require.NoError(t, err)

			// Run the test
			testFuncs, err := FindTestFunctions(tt.pkgPath, tmpDir)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected, testFuncs)
		})
	}
}

func TestFindTestFunctionsErrors(t *testing.T) {
	tests := []struct {
		name       string
		pkgPath    string
		workingDir string
		setup      func(string) error
		wantErr    string
	}{
		{
			name:       "missing go.mod for module path",
			pkgPath:    "github.com/test/module/pkg",
			workingDir: "nonexistent",
			wantErr:    "failed to find go.mod",
		},
		{
			name:       "invalid go.mod",
			pkgPath:    "github.com/test/module/pkg",
			workingDir: "testdata/invalid-mod",
			setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "go.mod"), []byte("invalid content"), 0644)
			},
			wantErr: "failed to parse go.mod",
		},
		{
			name:       "package not in module",
			pkgPath:    "github.com/other/module/pkg",
			workingDir: "testdata/valid-mod",
			setup: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/test/module\n\ngo 1.21\n"), 0644)
			},
			wantErr: "package github.com/other/module/pkg is not in module github.com/test/module",
		},
		{
			name:       "relative path not found",
			pkgPath:    "./nonexistent",
			workingDir: ".",
			wantErr:    "failed to read package directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for the test
			tmpDir := t.TempDir()

			// Run setup if provided
			if tt.setup != nil {
				err := tt.setup(tmpDir)
				require.NoError(t, err)
			}

			// Run the test
			_, err := FindTestFunctions(tt.pkgPath, tmpDir)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestFindTestPackages(t *testing.T) {
	// Create temporary directory for the test
	tmpDir := t.TempDir()

	// Create a structure with multiple test packages
	// tmpDir/
	//   ├── pkg1/
	//   │   └── pkg1_test.go
	//   ├── pkg2/
	//   │   └── pkg2_test.go
	//   ├── subdir/
	//   │   └── pkg3/
	//   │       └── pkg3_test.go
	//   └── regular_file.go (not a test)

	// Create directories
	pkg1Dir := filepath.Join(tmpDir, "pkg1")
	pkg2Dir := filepath.Join(tmpDir, "pkg2")
	pkg3Dir := filepath.Join(tmpDir, "subdir", "pkg3")

	require.NoError(t, os.MkdirAll(pkg1Dir, 0755))
	require.NoError(t, os.MkdirAll(pkg2Dir, 0755))
	require.NoError(t, os.MkdirAll(pkg3Dir, 0755))

	// Create test files
	testContent := `package test
import "testing"
func TestExample(t *testing.T) {}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkg1Dir, "pkg1_test.go"), []byte(testContent), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg2Dir, "pkg2_test.go"), []byte(testContent), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg3Dir, "pkg3_test.go"), []byte(testContent), 0644))

	// Create a non-test file (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "regular_file.go"), []byte("package main"), 0644))

	// Test finding packages
	packages, err := FindTestPackages(tmpDir, tmpDir)
	require.NoError(t, err)

	// Should find all three test packages
	expected := []string{"./pkg1", "./pkg2", "./subdir/pkg3"}
	require.ElementsMatch(t, expected, packages)
}

func TestFindTestPackagesWithEllipsis(t *testing.T) {
	// Create temporary directory for the test
	tmpDir := t.TempDir()

	// Create test package
	pkgDir := filepath.Join(tmpDir, "testpkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0755))

	testContent := `package test
import "testing"
func TestExample(t *testing.T) {}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "test_test.go"), []byte(testContent), 0644))

	// Test with "..." notation
	packages, err := FindTestPackages(tmpDir+"/...", tmpDir)
	require.NoError(t, err)

	expected := []string{"./testpkg"}
	require.ElementsMatch(t, expected, packages)
}

func TestFindTestPackagesEmpty(t *testing.T) {
	// Create temporary directory with no test files
	tmpDir := t.TempDir()

	packages, err := FindTestPackages(tmpDir, tmpDir)
	require.NoError(t, err)
	require.Empty(t, packages)
}

func TestFindTestPackagesNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "nonexistent")

	_, err := FindTestPackages(nonExistentDir, tmpDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

// Helper function to create test files
func createTestFiles(pkgDir string) error {
	testFiles := map[string]string{
		"normal_test.go": `
package pkg

func TestNormal(t *testing.T) {}
func TestAnother(t *testing.T) {}
`,
		"main_test.go": `
package pkg

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestWithMain(t *testing.T) {}
`,
		"benchmark_test.go": `
package pkg

func BenchmarkSomething(b *testing.B) {}
func TestWithBenchmark(t *testing.T) {}
`,
	}

	for filename, content := range testFiles {
		if err := os.WriteFile(filepath.Join(pkgDir, filename), []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}
