package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/infra/op-nat/types"
)

func TestParseTestTags(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    *types.ValidatorMetadata
	}{
		{
			name:    "valid_full_metadata",
			comment: "id:test1 type:test gate:gate1 suite:suite1",
			want: &types.ValidatorMetadata{
				ID:    "test1",
				Type:  types.ValidatorTypeTest,
				Gate:  "gate1",
				Suite: "suite1",
			},
		},
		{
			name:    "valid_gate",
			comment: "id:gate1 type:gate",
			want: &types.ValidatorMetadata{
				ID:   "gate1",
				Type: types.ValidatorTypeGate,
			},
		},
		{
			name:    "valid_suite",
			comment: "id:suite1 type:suite gate:gate1",
			want: &types.ValidatorMetadata{
				ID:   "suite1",
				Type: types.ValidatorTypeSuite,
				Gate: "gate1",
			},
		},
		{
			name:    "malformed_tag_is_skipped",
			comment: "id:test1 type:test gate:gate1 badtag suite:suite1",
			want: &types.ValidatorMetadata{
				ID:    "test1",
				Type:  types.ValidatorTypeTest,
				Gate:  "gate1",
				Suite: "suite1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseValidatorTags(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDiscoverTests(t *testing.T) {
	validators, err := DiscoverTests("testdata/validators")
	require.NoError(t, err)
	require.Len(t, validators, 4, "Should find 4 validators")
	output := ValidatorHierarchyString(validators)
	t.Log(output)

	// Map validators by ID for easier testing
	validatorMap := make(map[string]types.ValidatorMetadata)
	for _, v := range validators {
		validatorMap[v.ID] = v
	}

	// Verify gate
	gate, exists := validatorMap["gate1"]
	require.True(t, exists)
	assert.Equal(t, types.ValidatorTypeGate, gate.Type)
	assert.Empty(t, gate.Gate)
	assert.Empty(t, gate.Suite)

	// Verify suite
	suite, exists := validatorMap["suite1"]
	require.True(t, exists)
	assert.Equal(t, types.ValidatorTypeSuite, suite.Type)
	assert.Equal(t, "gate1", suite.Gate)
	assert.Empty(t, suite.Suite)

	// Verify tests
	test1, exists := validatorMap["test1"]
	require.True(t, exists)
	assert.Equal(t, types.ValidatorTypeTest, test1.Type)
	assert.Equal(t, "gate1", test1.Gate)
	assert.Equal(t, "suite1", test1.Suite)

	test2, exists := validatorMap["test2"]
	require.True(t, exists)
	assert.Equal(t, types.ValidatorTypeTest, test2.Type)
	assert.Equal(t, "gate1", test2.Gate)
	assert.Empty(t, test2.Suite)
}

func TestDiscoverAndRunTests(t *testing.T) {
	// Discover tests from testdata directory
	discoveredValidators, err := DiscoverTests("testdata/validators")
	require.NoError(t, err)
	require.Len(t, discoveredValidators, 4, "should discover 4 validators")

	// Verify the structure of discovered validators
	var gate, suite, testInSuite, testDirect types.ValidatorMetadata

	for _, v := range discoveredValidators {
		switch v.ID {
		case "gate1":
			gate = v
		case "suite1":
			suite = v
		case "test1":
			testInSuite = v
		case "test2":
			testDirect = v
		}
	}

	// Verify gate
	assert.Equal(t, types.ValidatorTypeGate, gate.Type)
	assert.Empty(t, gate.Gate)
	assert.Empty(t, gate.Suite)

	// Verify suite
	assert.Equal(t, types.ValidatorTypeSuite, suite.Type)
	assert.Equal(t, "gate1", suite.Gate)
	assert.Empty(t, suite.Suite)

	// Verify test in suite
	assert.Equal(t, types.ValidatorTypeTest, testInSuite.Type)
	assert.Equal(t, "gate1", testInSuite.Gate)
	assert.Equal(t, "suite1", testInSuite.Suite)

	// Verify direct test
	assert.Equal(t, types.ValidatorTypeTest, testDirect.Type)
	assert.Equal(t, "gate1", testDirect.Gate)
	assert.Empty(t, testDirect.Suite)
}
