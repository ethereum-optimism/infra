package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseTestNameHierarchy(t *testing.T) {
	tests := []struct {
		name          string
		testName      string
		expectedDepth int
		expectedPath  []string
	}{
		{
			name:          "empty string",
			testName:      "",
			expectedDepth: 0,
			expectedPath:  []string{},
		},
		{
			name:          "simple test",
			testName:      "TestSimple",
			expectedDepth: 0,
			expectedPath:  []string{"TestSimple"},
		},
		{
			name:          "first level subtest",
			testName:      "TestParent/SubTest",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
		},
		{
			name:          "second level subtest",
			testName:      "TestParent/SubTest/SubSubTest",
			expectedDepth: 2,
			expectedPath:  []string{"TestParent", "SubTest", "SubSubTest"},
		},
		{
			name:          "deep nesting",
			testName:      "TestA/B/C/D/E",
			expectedDepth: 4,
			expectedPath:  []string{"TestA", "B", "C", "D", "E"},
		},
		{
			name:          "trailing slash",
			testName:      "TestParent/SubTest/",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
		},
		{
			name:          "leading slash",
			testName:      "/TestParent/SubTest",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
		},
		{
			name:          "multiple slashes",
			testName:      "TestParent//SubTest",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth, path := ParseTestNameHierarchy(tt.testName)
			assert.Equal(t, tt.expectedDepth, depth, "Depth should match")
			assert.Equal(t, tt.expectedPath, path, "Path should match")
		})
	}
}

func TestBuildHierarchyPath(t *testing.T) {
	tests := []struct {
		name         string
		testNames    []string
		expectedPath []string
	}{
		{
			name:         "empty input",
			testNames:    []string{},
			expectedPath: []string{},
		},
		{
			name:         "single test",
			testNames:    []string{"TestSimple"},
			expectedPath: []string{"TestSimple"},
		},
		{
			name:         "multiple tests",
			testNames:    []string{"TestParent", "SubTest", "SubSubTest"},
			expectedPath: []string{"TestParent", "SubTest", "SubSubTest"},
		},
		{
			name:         "with empty strings",
			testNames:    []string{"TestParent", "", "SubTest", ""},
			expectedPath: []string{"TestParent", "SubTest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := BuildHierarchyPath(tt.testNames...)
			assert.Equal(t, tt.expectedPath, path)
		})
	}
}

func TestValidateHierarchyPath(t *testing.T) {
	tests := []struct {
		name          string
		path          []string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid single element",
			path:        []string{"TestSimple"},
			expectError: false,
		},
		{
			name:        "valid multiple elements",
			path:        []string{"TestParent", "SubTest", "SubSubTest"},
			expectError: false,
		},
		{
			name:          "empty path",
			path:          []string{},
			expectError:   true,
			errorContains: "cannot be empty",
		},
		{
			name:          "empty element",
			path:          []string{"TestParent", "", "SubTest"},
			expectError:   true,
			errorContains: "element at index 1 cannot be empty",
		},
		{
			name:          "element with slash",
			path:          []string{"TestParent", "Sub/Test"},
			expectError:   true,
			errorContains: "cannot contain '/' character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHierarchyPath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCalculateDepthFromPath(t *testing.T) {
	tests := []struct {
		name          string
		path          []string
		expectedDepth int
	}{
		{
			name:          "empty path",
			path:          []string{},
			expectedDepth: 0,
		},
		{
			name:          "single element",
			path:          []string{"TestSimple"},
			expectedDepth: 0,
		},
		{
			name:          "two elements",
			path:          []string{"TestParent", "SubTest"},
			expectedDepth: 1,
		},
		{
			name:          "three elements",
			path:          []string{"TestParent", "SubTest", "SubSubTest"},
			expectedDepth: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth := CalculateDepthFromPath(tt.path)
			assert.Equal(t, tt.expectedDepth, depth)
		})
	}
}

func TestTestResult_SetHierarchyInfo(t *testing.T) {
	tests := []struct {
		name          string
		depth         int
		path          []string
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid top-level test",
			depth:       0,
			path:        []string{"TestSimple"},
			expectError: false,
		},
		{
			name:        "valid subtest",
			depth:       1,
			path:        []string{"TestParent", "SubTest"},
			expectError: false,
		},
		{
			name:        "valid deep subtest",
			depth:       3,
			path:        []string{"TestA", "SubB", "SubSubC", "SubSubSubD"},
			expectError: false,
		},
		{
			name:          "empty path",
			depth:         0,
			path:          []string{},
			expectError:   true,
			errorContains: "cannot be empty",
		},
		{
			name:          "depth mismatch - too high",
			depth:         2,
			path:          []string{"TestParent", "SubTest"},
			expectError:   true,
			errorContains: "depth 2 does not match path length (expected 1",
		},
		{
			name:          "depth mismatch - too low",
			depth:         0,
			path:          []string{"TestParent", "SubTest"},
			expectError:   true,
			errorContains: "depth 0 does not match path length (expected 1",
		},
		{
			name:          "invalid path element",
			depth:         1,
			path:          []string{"TestParent", "Sub/Test"},
			expectError:   true,
			errorContains: "cannot contain '/' character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TestResult{}
			err := tr.SetHierarchyInfo(tt.depth, tt.path)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.depth, tr.Depth)
				assert.Equal(t, tt.path, tr.HierarchyPath)
				assert.Equal(t, tt.depth > 0, tr.IsSubTest)
			}
		})
	}
}

func TestTestResult_SetHierarchyInfoUnsafe(t *testing.T) {
	tr := &TestResult{}
	depth := 2
	path := []string{"TestParent", "SubTest", "SubSubTest"}

	tr.SetHierarchyInfoUnsafe(depth, path)

	assert.Equal(t, depth, tr.Depth)
	assert.Equal(t, path, tr.HierarchyPath)
	assert.True(t, tr.IsSubTest)

	// Verify the path is copied, not referenced
	path[0] = "Modified"
	assert.Equal(t, "TestParent", tr.HierarchyPath[0])
}

func TestTestResult_SetHierarchyFromTestName(t *testing.T) {
	tests := []struct {
		name          string
		testName      string
		expectedDepth int
		expectedPath  []string
		expectedSub   bool
	}{
		{
			name:          "simple test",
			testName:      "TestSimple",
			expectedDepth: 0,
			expectedPath:  []string{"TestSimple"},
			expectedSub:   false,
		},
		{
			name:          "subtest",
			testName:      "TestParent/SubTest",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
			expectedSub:   true,
		},
		{
			name:          "deep subtest",
			testName:      "TestA/B/C/D",
			expectedDepth: 3,
			expectedPath:  []string{"TestA", "B", "C", "D"},
			expectedSub:   true,
		},
		{
			name:          "empty test name",
			testName:      "",
			expectedDepth: 0,
			expectedPath:  []string{},
			expectedSub:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TestResult{}
			tr.SetHierarchyFromTestName(tt.testName)

			assert.Equal(t, tt.expectedDepth, tr.Depth)
			assert.Equal(t, tt.expectedPath, tr.HierarchyPath)
			assert.Equal(t, tt.expectedSub, tr.IsSubTest)
		})
	}
}

func TestTestResult_GetParentPath(t *testing.T) {
	tests := []struct {
		name          string
		hierarchyPath []string
		expectedPath  []string
	}{
		{
			name:          "empty path",
			hierarchyPath: []string{},
			expectedPath:  nil,
		},
		{
			name:          "single element",
			hierarchyPath: []string{"TestSimple"},
			expectedPath:  nil,
		},
		{
			name:          "two elements",
			hierarchyPath: []string{"TestParent", "SubTest"},
			expectedPath:  []string{"TestParent"},
		},
		{
			name:          "three elements",
			hierarchyPath: []string{"TestA", "SubB", "SubSubC"},
			expectedPath:  []string{"TestA", "SubB"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TestResult{
				HierarchyPath: tt.hierarchyPath,
			}
			parentPath := tr.GetParentPath()
			assert.Equal(t, tt.expectedPath, parentPath)
		})
	}
}

func TestTestResult_GetParentName(t *testing.T) {
	tests := []struct {
		name          string
		hierarchyPath []string
		expectedName  string
	}{
		{
			name:          "empty path",
			hierarchyPath: []string{},
			expectedName:  "",
		},
		{
			name:          "single element",
			hierarchyPath: []string{"TestSimple"},
			expectedName:  "",
		},
		{
			name:          "two elements",
			hierarchyPath: []string{"TestParent", "SubTest"},
			expectedName:  "TestParent",
		},
		{
			name:          "three elements",
			hierarchyPath: []string{"TestA", "SubB", "SubSubC"},
			expectedName:  "SubB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TestResult{
				HierarchyPath: tt.hierarchyPath,
			}
			parentName := tr.GetParentName()
			assert.Equal(t, tt.expectedName, parentName)
		})
	}
}

func TestTestResult_GetFullTestPath(t *testing.T) {
	tests := []struct {
		name          string
		hierarchyPath []string
		expectedPath  string
	}{
		{
			name:          "empty path",
			hierarchyPath: []string{},
			expectedPath:  "",
		},
		{
			name:          "single element",
			hierarchyPath: []string{"TestSimple"},
			expectedPath:  "TestSimple",
		},
		{
			name:          "multiple elements",
			hierarchyPath: []string{"TestParent", "SubTest", "SubSubTest"},
			expectedPath:  "TestParent/SubTest/SubSubTest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &TestResult{
				HierarchyPath: tt.hierarchyPath,
			}
			fullPath := tr.GetFullTestPath()
			assert.Equal(t, tt.expectedPath, fullPath)
		})
	}
}

func TestGetTestDisplayName(t *testing.T) {
	tests := []struct {
		name         string
		testName     string
		metadata     ValidatorMetadata
		expectedName string
	}{
		{
			name:     "test with name",
			testName: "TestFunction",
			metadata: ValidatorMetadata{
				Package: "github.com/example/test",
			},
			expectedName: "TestFunction",
		},
		{
			name:     "empty name with package",
			testName: "",
			metadata: ValidatorMetadata{
				Package: "github.com/example/test",
			},
			expectedName: "test (package)",
		},
		{
			name:     "empty name with simple package",
			testName: "",
			metadata: ValidatorMetadata{
				Package: "test",
			},
			expectedName: "test (package)",
		},
		{
			name:         "empty name and package",
			testName:     "",
			metadata:     ValidatorMetadata{},
			expectedName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			displayName := GetTestDisplayName(tt.testName, tt.metadata)
			assert.Equal(t, tt.expectedName, displayName)
		})
	}
}

// Integration test to verify complete hierarchy workflow
func TestHierarchyIntegration(t *testing.T) {
	// Create a test result with complex hierarchy
	tr := &TestResult{
		Metadata: ValidatorMetadata{
			ID:       "test-deep",
			FuncName: "TestParent/SubTest1/SubSubTest/DeepTest",
			Package:  "github.com/example/test",
			Gate:     "test-gate",
		},
		Status:   TestStatusPass,
		Duration: 100 * time.Millisecond,
	}

	// Set hierarchy from test name
	tr.SetHierarchyFromTestName(tr.Metadata.FuncName)

	// Verify all hierarchy information is correct
	assert.Equal(t, 3, tr.Depth)
	expectedPath := []string{"TestParent", "SubTest1", "SubSubTest", "DeepTest"}
	assert.Equal(t, expectedPath, tr.HierarchyPath)
	assert.True(t, tr.IsSubTest)

	// Test parent relationships
	assert.Equal(t, "SubSubTest", tr.GetParentName())
	assert.Equal(t, []string{"TestParent", "SubTest1", "SubSubTest"}, tr.GetParentPath())
	assert.Equal(t, "TestParent/SubTest1/SubSubTest/DeepTest", tr.GetFullTestPath())

	// Test validation
	err := tr.SetHierarchyInfo(tr.Depth, tr.HierarchyPath)
	assert.NoError(t, err, "Current hierarchy should be valid")

	// Test invalid depth
	err = tr.SetHierarchyInfo(2, tr.HierarchyPath)
	assert.Error(t, err, "Should reject incorrect depth")
}
