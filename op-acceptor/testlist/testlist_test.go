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
			wantErr:    "failed to read go.mod",
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
