package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGateConfig_ResolveInherited(t *testing.T) {
	tests := []struct {
		name    string
		gates   map[string]GateConfig
		gateID  string
		want    GateConfig
		wantErr string
	}{
		{
			name: "single level inheritance",
			gates: map[string]GateConfig{
				"parent": {
					ID: "parent",
					Tests: []TestConfig{
						{Name: "ParentTest", Package: "pkg/parent"},
					},
					Suites: map[string]SuiteConfig{
						"parent-suite": {
							Description: "Parent suite",
							Tests: []TestConfig{
								{Name: "ParentSuiteTest", Package: "pkg/parent"},
							},
						},
					},
				},
				"child": {
					ID:       "child",
					Inherits: []string{"parent"},
					Tests: []TestConfig{
						{Name: "ChildTest", Package: "pkg/child"},
					},
				},
			},
			gateID: "child",
			want: GateConfig{
				ID:       "child",
				Inherits: []string{"parent"},
				Tests: []TestConfig{
					{Name: "ChildTest", Package: "pkg/child"},
					{Name: "ParentTest", Package: "pkg/parent"},
				},
				Suites: map[string]SuiteConfig{
					"parent-suite": {
						Description: "Parent suite",
						Tests: []TestConfig{
							{Name: "ParentSuiteTest", Package: "pkg/parent"},
						},
					},
				},
			},
		},
		{
			name: "multi-level inheritance",
			gates: map[string]GateConfig{
				"grandparent": {
					ID: "grandparent",
					Tests: []TestConfig{
						{Name: "GrandparentTest", Package: "pkg/grandparent"},
					},
					Suites: map[string]SuiteConfig{
						"grandparent-suite": {
							Tests: []TestConfig{
								{Name: "GrandparentSuiteTest", Package: "pkg/grandparent"},
							},
						},
					},
				},
				"parent": {
					ID:       "parent",
					Inherits: []string{"grandparent"},
					Tests: []TestConfig{
						{Name: "ParentTest", Package: "pkg/parent"},
					},
					Suites: map[string]SuiteConfig{
						"parent-suite": {
							Tests: []TestConfig{
								{Name: "ParentSuiteTest", Package: "pkg/parent"},
							},
						},
					},
				},
				"child": {
					ID:       "child",
					Inherits: []string{"parent"},
					Tests: []TestConfig{
						{Name: "ChildTest", Package: "pkg/child"},
					},
				},
			},
			gateID: "child",
			want: GateConfig{
				ID:       "child",
				Inherits: []string{"parent"},
				Tests: []TestConfig{
					{Name: "ChildTest", Package: "pkg/child"},
					{Name: "ParentTest", Package: "pkg/parent"},
					{Name: "GrandparentTest", Package: "pkg/grandparent"},
				},
				Suites: map[string]SuiteConfig{
					"grandparent-suite": {
						Tests: []TestConfig{
							{Name: "GrandparentSuiteTest", Package: "pkg/grandparent"},
						},
					},
					"parent-suite": {
						Tests: []TestConfig{
							{Name: "ParentSuiteTest", Package: "pkg/parent"},
						},
					},
				},
			},
		},
		{
			name: "suite override in child",
			gates: map[string]GateConfig{
				"parent": {
					ID: "parent",
					Suites: map[string]SuiteConfig{
						"test-suite": {
							Description: "Parent suite",
							Tests: []TestConfig{
								{Name: "ParentTest", Package: "pkg/parent"},
							},
						},
					},
				},
				"child": {
					ID:       "child",
					Inherits: []string{"parent"},
					Suites: map[string]SuiteConfig{
						"test-suite": {
							Description: "Child suite",
							Tests: []TestConfig{
								{Name: "ChildTest", Package: "pkg/child"},
							},
						},
					},
				},
			},
			gateID: "child",
			want: GateConfig{
				ID:       "child",
				Inherits: []string{"parent"},
				Suites: map[string]SuiteConfig{
					"test-suite": {
						Description: "Child suite",
						Tests: []TestConfig{
							{Name: "ChildTest", Package: "pkg/child"},
						},
					},
				},
			},
		},
		{
			name: "circular inheritance",
			gates: map[string]GateConfig{
				"gate1": {
					ID:       "gate1",
					Inherits: []string{"gate2"},
				},
				"gate2": {
					ID:       "gate2",
					Inherits: []string{"gate1"},
				},
			},
			gateID:  "gate1",
			wantErr: `circular inheritance detected for gate "gate2"`,
		},
		{
			name: "non-existent parent",
			gates: map[string]GateConfig{
				"child": {
					ID:       "child",
					Inherits: []string{"missing-parent"},
				},
			},
			gateID:  "child",
			wantErr: `gate "child" inherits from non-existent gate "missing-parent"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := tt.gates[tt.gateID]
			err := gate.ResolveInherited(tt.gates)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.Tests, gate.Tests)
			assert.Equal(t, tt.want.Suites, gate.Suites)
		})
	}
}
