package testlist

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// FindTestFunctions takes a package path and working directory, and returns a list of test function names
func FindTestFunctions(pkgPath string, workingDir string) ([]string, error) {
	var relPath string

	// Check if pkgPath is already a relative path
	if strings.HasPrefix(pkgPath, "./") {
		relPath = strings.TrimPrefix(pkgPath, "./")
	} else {
		// Read and parse go.mod
		goModPath := filepath.Join(workingDir, "go.mod")
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

		relPath = strings.TrimPrefix(pkgPath, moduleName)
		if relPath == "" {
			relPath = "."
		}
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
