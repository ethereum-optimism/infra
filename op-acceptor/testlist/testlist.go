package testlist

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// FindTestPackages recursively discovers all directories containing *_test.go files
// within the given directory. It returns a list of relative package paths that can
// be used with "go test" commands. It supports Go's "..." notation for the input path.
func FindTestPackages(testDir string, workingDir string) ([]string, error) {
	// Handle "..." notation (e.g., "./acceptance-tests/...")
	testDir = strings.TrimSuffix(testDir, "/...")

	// Normalize workingDir to absolute for consistent processing
	absWorkingDir := workingDir
	if !filepath.IsAbs(absWorkingDir) {
		if wdAbs, err := filepath.Abs(absWorkingDir); err == nil {
			absWorkingDir = wdAbs
		}
	}

	// Convert to absolute search path for consistent processing
	var searchPath string
	if filepath.IsAbs(testDir) {
		searchPath = testDir
	} else {
		searchPath = filepath.Join(absWorkingDir, testDir)
	}

	// Clean the path to avoid issues with ".." components
	searchPath = filepath.Clean(searchPath)

	// Verify the search path exists
	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("test directory does not exist: %s", searchPath)
	}

	var packages []string

	// Ensure searchPath is absolute for walking
	if !filepath.IsAbs(searchPath) {
		if spAbs, err := filepath.Abs(searchPath); err == nil {
			searchPath = spAbs
		}
	}

	// Walk the directory tree
	err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip if this isn't a directory
		if !d.IsDir() {
			return nil
		}

		// Check if this directory contains test files
		hasTestFiles, err := hasGoTestFiles(path)
		if err != nil {
			return err
		}

		if hasTestFiles {
			// Try to get the Go package name using 'go list'
			packageName, err := getGoPackageName(path)
			if err == nil && packageName != "" {
				packages = append(packages, packageName)
				return nil
			}

			// Fallback: relative path from absWorkingDir, normalized to avoid ".." components
			relPath, relErr := filepath.Rel(absWorkingDir, path)
			if relErr != nil {
				return fmt.Errorf("failed to get relative path for %s: %w", path, relErr)
			}
			relPath = filepath.Clean(relPath)

			if relPath == "." {
				relPath = filepath.Base(path)
			} else if !strings.HasPrefix(relPath, "./") && !strings.HasPrefix(relPath, "../") {
				relPath = "./" + relPath
			}

			packages = append(packages, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk test directory: %w", err)
	}

	return packages, nil
}

// hasGoTestFiles checks if a directory contains any *_test.go files
func hasGoTestFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			return true, nil
		}
	}

	return false, nil
}

// getGoPackageName uses 'go list' to get the proper Go package name for a directory
func getGoPackageName(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}")
	cmd.Dir = dir

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	if err != nil {
		// Return error with stderr output for debugging
		return "", fmt.Errorf("go list failed: %w (stderr: %s)", err, errOut.String())
	}

	packageName := strings.TrimSpace(out.String())
	if packageName == "" {
		return "", fmt.Errorf("go list returned empty package name")
	}

	return packageName, nil
}

// FindTestFunctions takes a package path and working directory, and returns a list of test function names
func FindTestFunctions(pkgPath string, workingDir string) ([]string, error) {
	var relPath string

	// Check if pkgPath is already a relative path
	if strings.HasPrefix(pkgPath, "./") {
		relPath = strings.TrimPrefix(pkgPath, "./")
	} else {
		// Find go.mod by searching up the directory tree from workingDir
		goModPath, err := findGoMod(workingDir)
		if err != nil {
			return nil, fmt.Errorf("failed to find go.mod: %w", err)
		}

		goModContent, err := os.ReadFile(goModPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read go.mod: %w", err)
		}

		modFile, err := modfile.Parse(goModPath, goModContent, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to parse go.mod: %w", err)
		}

		moduleName := modFile.Module.Mod.Path
		if moduleName == "" {
			return nil, fmt.Errorf("could not find module name in go.mod")
		}

		// Verify that the package is indeed in the module
		if !strings.HasPrefix(pkgPath, moduleName) {
			return nil, fmt.Errorf("package %s is not in module %s", pkgPath, moduleName)
		}

		// Get the directory containing go.mod (module root)
		moduleRoot := filepath.Dir(goModPath)

		// Calculate relative path from module root to package
		relPath = strings.TrimPrefix(pkgPath, moduleName)
		if relPath == "" {
			relPath = "."
		} else if strings.HasPrefix(relPath, "/") {
			relPath = strings.TrimPrefix(relPath, "/")
		}

		// Update workingDir to be the module root for path resolution
		workingDir = moduleRoot
	}

	// Find all test files in the package directory
	pkgDir := filepath.Join(workingDir, relPath)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read package directory: %w", err)
	}

	var testFunctions []string
	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		f, err := parser.ParseFile(fset, filePath, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", entry.Name(), err)
		}

		// Traverse top-level declarations in search of test functions
		for _, decl := range f.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			// Those functions have to start with "Test" and not be "TestMain"
			if strings.HasPrefix(funcDecl.Name.Name, "Test") && funcDecl.Name.Name != "TestMain" {
				testFunctions = append(testFunctions, funcDecl.Name.Name)
			}
		}
	}

	return testFunctions, nil
}

// findGoMod searches for go.mod file starting from the given directory and moving up the directory tree
func findGoMod(startDir string) (string, error) {
	dir := startDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return goModPath, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root directory
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("go.mod not found in %s or any parent directory", startDir)
}
