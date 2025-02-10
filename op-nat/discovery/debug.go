package discovery

import (
	"fmt"
	"strings"

	"github.com/ethereum-optimism/infra/op-nat/types"
)

// ValidatorHierarchyString returns a string representation of the validator hierarchy
func ValidatorHierarchyString(validators []types.ValidatorMetadata) string {
	var sb strings.Builder
	sb.WriteString("Validator Hierarchy:\n")

	// Group validators by gate
	gateMap := make(map[string][]types.ValidatorMetadata)
	for _, v := range validators {
		gateMap[v.Gate] = append(gateMap[v.Gate], v)
	}

	// Process each gate
	for gate, gateValidators := range gateMap {
		sb.WriteString(fmt.Sprintf("└── Gate: %s\n", gate))

		// Group by suite
		suiteMap := make(map[string][]types.ValidatorMetadata)
		var directTests []types.ValidatorMetadata
		for _, v := range gateValidators {
			if v.Suite == "" {
				directTests = append(directTests, v)
			} else {
				suiteMap[v.Suite] = append(suiteMap[v.Suite], v)
			}
		}

		// Print direct tests
		if len(directTests) > 0 {
			sb.WriteString("    ├── Direct Tests:\n")
			for i, test := range directTests {
				if i == len(directTests)-1 {
					sb.WriteString(fmt.Sprintf("    │   └── %s\n", test.ID))
				} else {
					sb.WriteString(fmt.Sprintf("    │   ├── %s\n", test.ID))
				}
			}
		}

		// Print suites
		if len(suiteMap) > 0 {
			sb.WriteString("    └── Suites:\n")
			for suite, tests := range suiteMap {
				sb.WriteString(fmt.Sprintf("        └── %s\n", suite))
				for i, test := range tests {
					if i == len(tests)-1 {
						sb.WriteString(fmt.Sprintf("            └── %s\n", test.ID))
					} else {
						sb.WriteString(fmt.Sprintf("            ├── %s\n", test.ID))
					}
				}
			}
		}
	}

	return sb.String()
}
