package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum-optimism/infra/op-nat/types"
)

func TestRegistry(t *testing.T) {
	// Create test directory structure
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create test validator config
	validConfig := `
gates:
  - id: test-gate
    tests:
      - id: test1
        name: "Test 1"
    suites:
      test-suite:
        tests:
          - id: suite-test1
            name: "Suite Test 1"
`
	err = os.WriteFile(filepath.Join(configDir, "validators.yaml"), []byte(validConfig), 0644)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("source loading", func(t *testing.T) {
		tests := []struct {
			name    string
			cfg     Config
			wantErr bool
		}{
			{
				name: "valid local source",
				cfg: Config{
					Source: types.SourceConfig{
						Location:   tmpDir,
						ConfigPath: "config/validators.yaml",
					},
					WorkDir: tmpDir,
				},
				wantErr: false,
			},
			{
				name: "invalid config path",
				cfg: Config{
					Source: types.SourceConfig{
						Location:   tmpDir,
						ConfigPath: "nonexistent.yaml",
					},
					WorkDir: tmpDir,
				},
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r, err := NewRegistry(tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("NewRegistry() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if err == nil {
					if r.sources[tt.cfg.Source.Location] == nil {
						t.Error("source not loaded")
					}
				}
			})
		}
	})

	t.Run("gate operations", func(t *testing.T) {
		cfg := Config{
			Source: types.SourceConfig{
				Location:   tmpDir,
				ConfigPath: "config/validators.yaml",
			},
			WorkDir: tmpDir,
		}

		r, err := NewRegistry(cfg)
		if err != nil {
			t.Fatal(err)
		}

		t.Run("get existing gate", func(t *testing.T) {
			gate := r.GetGate("test-gate")
			if gate == nil {
				t.Error("expected to find test-gate")
			}
		})

		t.Run("get non-existent gate", func(t *testing.T) {
			gate := r.GetGate("nonexistent-gate")
			if gate != nil {
				t.Error("expected nil for non-existent gate")
			}
		})

		t.Run("add new gate", func(t *testing.T) {
			gate := r.AddGate("new-gate")
			if gate == nil {
				t.Error("expected non-nil gate")
			}
			if gate.ID != "new-gate" {
				t.Errorf("expected gate ID 'new-gate', got %s", gate.ID)
			}
		})
	})

	t.Run("validate", func(t *testing.T) {
		cfg := Config{
			Source: types.SourceConfig{
				Location:   tmpDir,
				ConfigPath: "config/validators.yaml",
			},
			WorkDir: tmpDir,
		}

		r, err := NewRegistry(cfg)
		if err != nil {
			t.Fatal(err)
		}

		if err := r.Validate(); err != nil {
			t.Errorf("Validate() returned error: %v", err)
		}
	})

	t.Run("get config", func(t *testing.T) {
		cfg := Config{
			Source: types.SourceConfig{
				Location:   tmpDir,
				ConfigPath: "config/validators.yaml",
			},
			WorkDir: tmpDir,
			Gate:    "test-gate",
		}

		r, err := NewRegistry(cfg)
		if err != nil {
			t.Fatal(err)
		}

		gotCfg := r.GetConfig()
		if gotCfg.Gate != cfg.Gate {
			t.Errorf("GetConfig().Gate = %v, want %v", gotCfg.Gate, cfg.Gate)
		}
		if gotCfg.WorkDir != cfg.WorkDir {
			t.Errorf("GetConfig().WorkDir = %v, want %v", gotCfg.WorkDir, cfg.WorkDir)
		}
	})
}
