package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ethereum-optimism/infra/op-nat/types"
)

// ValidatorHierarchyString returns a string representation of the validator hierarchy
func ValidatorHierarchyString(validators []types.ValidatorMetadata) string {
	var sb strings.Builder
	sb.WriteString("Validator Hierarchy:\n")

	// Group by type and gate
	gateMap := make(map[string]struct {
		gate   types.ValidatorMetadata
		suites []types.ValidatorMetadata
		tests  []types.ValidatorMetadata
	})

	for _, v := range validators {
		switch v.Type {
		case types.ValidatorTypeGate:
			if _, exists := gateMap[v.ID]; !exists {
				gateMap[v.ID] = struct {
					gate   types.ValidatorMetadata
					suites []types.ValidatorMetadata
					tests  []types.ValidatorMetadata
				}{gate: v}
			}
		case types.ValidatorTypeSuite:
			entry := gateMap[v.Gate]
			entry.suites = append(entry.suites, v)
			gateMap[v.Gate] = entry
		case types.ValidatorTypeTest:
			entry := gateMap[v.Gate]
			entry.tests = append(entry.tests, v)
			gateMap[v.Gate] = entry
		}
	}

	// Sort and print
	gates := make([]string, 0, len(gateMap))
	for id := range gateMap {
		gates = append(gates, id)
	}
	sort.Strings(gates)

	for _, gateID := range gates {
		g := gateMap[gateID]
		sb.WriteString(fmt.Sprintf("└── Gate: %s\n", gateID))

		// Direct tests
		directTests := filterTests(g.tests, "")
		for i, test := range directTests {
			prefix := "    ├── "
			if i == len(directTests)-1 && len(g.suites) == 0 {
				prefix = "    └── "
			}
			sb.WriteString(fmt.Sprintf("%s%s\n", prefix, test.ID))
		}

		// Suites and their tests
		for i, suite := range g.suites {
			prefix := "    ├── "
			if i == len(g.suites)-1 {
				prefix = "    └── "
			}
			sb.WriteString(fmt.Sprintf("%s%s\n", prefix, suite.ID))

			suiteTests := filterTests(g.tests, suite.ID)
			for j, test := range suiteTests {
				testPrefix := "    │   "
				if i == len(g.suites)-1 {
					testPrefix = "        "
				}
				if j == len(suiteTests)-1 {
					sb.WriteString(fmt.Sprintf("%s└── %s\n", testPrefix, test.ID))
				} else {
					sb.WriteString(fmt.Sprintf("%s├── %s\n", testPrefix, test.ID))
				}
			}
		}
	}

	return sb.String()
}

func filterTests(tests []types.ValidatorMetadata, suite string) []types.ValidatorMetadata {
	var filtered []types.ValidatorMetadata
	for _, t := range tests {
		if t.Suite == suite {
			filtered = append(filtered, t)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID < filtered[j].ID
	})
	return filtered
}
