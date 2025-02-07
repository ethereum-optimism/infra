package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintValidatorHierarchy(t *testing.T) {
	validators, err := DiscoverTests("testdata/validators")
	require.NoError(t, err)

	output := ValidatorHierarchyString(validators)
	expected := `Validator Hierarchy:
└── Gate: gate1
    ├── Direct Tests:
    │   └── test2
    └── Suites:
        └── suite1
            └── test1
`
	assert.Equal(t, expected, output)

	// Print the hierarchy for visual inspection during test development
	t.Log("\n" + output)
}
