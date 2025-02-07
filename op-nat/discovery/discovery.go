// package discovery is used to discover tests in a directory and return their metadata.
// The metadata is stored in the TestMetadata struct.
//
// The package uses the go/ast package to parse the tests and their metadata from the comments.
package discovery

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/ethereum-optimism/infra/op-nat/types"
)

const (
	ValidatorPrefix = "//nat:validator "
)

// DiscoverTests scans a directory for tests and their metadata
func DiscoverTests(testDir string) ([]types.ValidatorMetadata, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, testDir, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse directory: %w", err)
	}

	var validators []types.ValidatorMetadata
	idMap := make(map[string]struct{})

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				funcDecl, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}

				if funcDecl.Doc == nil {
					continue
				}

				if metadata := parseValidatorMetadata(funcDecl.Doc); metadata != nil {
					// Check for ID collision
					if _, exists := idMap[metadata.ID]; exists {
						return nil, fmt.Errorf("duplicate validator ID found: %s", metadata.ID)
					}
					idMap[metadata.ID] = struct{}{}

					metadata.FuncName = funcDecl.Name.String()
					validators = append(validators, *metadata)
				}
			}
		}
	}

	return validators, nil
}

func parseValidatorMetadata(doc *ast.CommentGroup) *types.ValidatorMetadata {
	for _, comment := range doc.List {
		if strings.HasPrefix(comment.Text, ValidatorPrefix) {
			return parseValidatorTags(strings.TrimPrefix(comment.Text, ValidatorPrefix))
		}
	}
	return nil
}

func parseValidatorTags(comment string) *types.ValidatorMetadata {
	metadata := &types.ValidatorMetadata{}

	// Split the comment into individual tags
	tags := strings.Fields(comment)

	for _, tag := range tags {
		parts := strings.Split(tag, ":")
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]
		switch key {
		case "id":
			metadata.ID = value
		case "type":
			switch value {
			case "test":
				metadata.Type = types.ValidatorTypeTest
			case "suite":
				metadata.Type = types.ValidatorTypeSuite
			case "gate":
				metadata.Type = types.ValidatorTypeGate
			default:
				// Invalid type, skip this validator
				return nil
			}
		case "gate":
			metadata.Gate = value
		case "suite":
			metadata.Suite = value
		}
	}

	// Validate the metadata
	if !isValidMetadata(metadata) {
		return nil
	}

	return metadata
}

// isValidMetadata checks if the metadata is valid according to our rules
func isValidMetadata(metadata *types.ValidatorMetadata) bool {
	// Must have an ID and Type
	if metadata.ID == "" || metadata.Type == "" {
		return false
	}

	// Validate based on type
	switch metadata.Type {
	case types.ValidatorTypeTest:
		// Tests must belong to a gate
		if metadata.Gate == "" {
			return false
		}
		// Suite is optional for tests
	case types.ValidatorTypeSuite:
		// Suites must belong to a gate and should not have a parent suite
		if metadata.Gate == "" || metadata.Suite != "" {
			return false
		}
	case types.ValidatorTypeGate:
		// Gates should not have parent gate or suite
		if metadata.Gate != "" || metadata.Suite != "" {
			return false
		}
	default:
		return false
	}

	return true
}
