package river

import (
	"errors"
	"testing"

	"github.com/lirancohen/skene/workflow"
)

// createTestWorkflow creates a minimal workflow definition for testing.
func createTestWorkflow(name string) *workflow.WorkflowDef {
	step := workflow.NewStep(name+"-step", func(ctx workflow.Context) (any, error) {
		return nil, nil
	})
	return workflow.Define(name, step.After())
}

// createTestWorkflowWithVersion creates a workflow with a specific version.
func createTestWorkflowWithVersion(name, version string) *workflow.WorkflowDef {
	return createTestWorkflow(name).WithVersion(version)
}

func TestRegistry(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Registry)
		operation func(*Registry) error
		check     func(*testing.T, *Registry)
		wantErr   error
	}{
		{
			name:  "new registry is empty",
			setup: func(r *Registry) {},
			check: func(t *testing.T, r *Registry) {
				if r.Count() != 0 {
					t.Errorf("expected 0 workflows, got %d", r.Count())
				}
			},
		},
		{
			name: "register and get workflow",
			setup: func(r *Registry) {
				r.Register(createTestWorkflow("test-workflow"))
			},
			operation: func(r *Registry) error {
				_, err := r.Get("test-workflow")
				return err
			},
			check: func(t *testing.T, r *Registry) {
				if r.Count() != 1 {
					t.Errorf("expected 1 workflow, got %d", r.Count())
				}
			},
		},
		{
			name:  "get unregistered workflow returns error",
			setup: func(r *Registry) {},
			operation: func(r *Registry) error {
				_, err := r.Get("nonexistent")
				return err
			},
			wantErr: ErrWorkflowNotFound,
		},
		{
			name: "register same version replaces existing",
			setup: func(r *Registry) {
				r.Register(createTestWorkflow("test-workflow"))
				r.Register(createTestWorkflow("test-workflow")) // same name, same version
			},
			check: func(t *testing.T, r *Registry) {
				if r.Count() != 1 {
					t.Errorf("expected 1 workflow, got %d", r.Count())
				}
				versions, err := r.Versions("test-workflow")
				if err != nil {
					t.Fatalf("Versions failed: %v", err)
				}
				if len(versions) != 1 {
					t.Errorf("expected 1 version, got %d", len(versions))
				}
			},
		},
		{
			name: "names returns all workflow names",
			setup: func(r *Registry) {
				r.Register(createTestWorkflow("workflow-a"))
				r.Register(createTestWorkflow("workflow-b"))
				r.Register(createTestWorkflow("workflow-c"))
			},
			check: func(t *testing.T, r *Registry) {
				names := r.Names()
				if len(names) != 3 {
					t.Errorf("expected 3 names, got %d", len(names))
				}
				// Check all names are present (order is not guaranteed)
				nameSet := make(map[string]bool)
				for _, n := range names {
					nameSet[n] = true
				}
				for _, expected := range []string{"workflow-a", "workflow-b", "workflow-c"} {
					if !nameSet[expected] {
						t.Errorf("expected name %q not found", expected)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			tt.setup(r)

			if tt.operation != nil {
				err := tt.operation(r)
				if tt.wantErr != nil {
					if !errors.Is(err, tt.wantErr) {
						t.Errorf("expected error %v, got %v", tt.wantErr, err)
					}
					return
				}
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}

			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
}

func TestRegistry_Versioning(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Registry)
		check     func(*testing.T, *Registry)
		wantErr   error
		operation func(*Registry) error
	}{
		{
			name: "register multiple versions",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
				r.Register(createTestWorkflowWithVersion("order", "v2"))
				r.Register(createTestWorkflowWithVersion("order", "v3"))
			},
			check: func(t *testing.T, r *Registry) {
				if r.Count() != 1 {
					t.Errorf("expected 1 workflow name, got %d", r.Count())
				}
				versions, err := r.Versions("order")
				if err != nil {
					t.Fatalf("Versions failed: %v", err)
				}
				if len(versions) != 3 {
					t.Errorf("expected 3 versions, got %d", len(versions))
				}
			},
		},
		{
			name: "first registered version becomes latest",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
				r.Register(createTestWorkflowWithVersion("order", "v2"))
			},
			check: func(t *testing.T, r *Registry) {
				latest, err := r.LatestVersion("order")
				if err != nil {
					t.Fatalf("LatestVersion failed: %v", err)
				}
				if latest != "v1" {
					t.Errorf("expected latest to be v1, got %s", latest)
				}
			},
		},
		{
			name: "get returns latest version",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
				r.Register(createTestWorkflowWithVersion("order", "v2"))
			},
			check: func(t *testing.T, r *Registry) {
				def, err := r.Get("order")
				if err != nil {
					t.Fatalf("Get failed: %v", err)
				}
				// First registered is latest by default
				if def.Version() != "v1" {
					t.Errorf("expected Get to return v1, got %s", def.Version())
				}
			},
		},
		{
			name: "get version returns specific version",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
				r.Register(createTestWorkflowWithVersion("order", "v2"))
			},
			check: func(t *testing.T, r *Registry) {
				def, err := r.GetVersion("order", "v2")
				if err != nil {
					t.Fatalf("GetVersion failed: %v", err)
				}
				if def.Version() != "v2" {
					t.Errorf("expected v2, got %s", def.Version())
				}
			},
		},
		{
			name: "get version for nonexistent version returns error",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
			},
			operation: func(r *Registry) error {
				_, err := r.GetVersion("order", "v99")
				return err
			},
			wantErr: ErrVersionNotFound,
		},
		{
			name: "get version for nonexistent workflow returns error",
			setup: func(r *Registry) {},
			operation: func(r *Registry) error {
				_, err := r.GetVersion("nonexistent", "v1")
				return err
			},
			wantErr: ErrWorkflowNotFound,
		},
		{
			name: "set latest changes default version",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
				r.Register(createTestWorkflowWithVersion("order", "v2"))
				r.Register(createTestWorkflowWithVersion("order", "v3"))
			},
			operation: func(r *Registry) error {
				return r.SetLatest("order", "v2")
			},
			check: func(t *testing.T, r *Registry) {
				def, err := r.Get("order")
				if err != nil {
					t.Fatalf("Get failed: %v", err)
				}
				if def.Version() != "v2" {
					t.Errorf("expected Get to return v2 after SetLatest, got %s", def.Version())
				}
				latest, err := r.LatestVersion("order")
				if err != nil {
					t.Fatalf("LatestVersion failed: %v", err)
				}
				if latest != "v2" {
					t.Errorf("expected latest to be v2, got %s", latest)
				}
			},
		},
		{
			name: "set latest with nonexistent version returns error",
			setup: func(r *Registry) {
				r.Register(createTestWorkflowWithVersion("order", "v1"))
			},
			operation: func(r *Registry) error {
				return r.SetLatest("order", "v99")
			},
			wantErr: ErrVersionNotFound,
		},
		{
			name: "set latest with nonexistent workflow returns error",
			setup: func(r *Registry) {},
			operation: func(r *Registry) error {
				return r.SetLatest("nonexistent", "v1")
			},
			wantErr: ErrWorkflowNotFound,
		},
		{
			name: "latest version for nonexistent workflow returns error",
			setup: func(r *Registry) {},
			operation: func(r *Registry) error {
				_, err := r.LatestVersion("nonexistent")
				return err
			},
			wantErr: ErrWorkflowNotFound,
		},
		{
			name: "versions for nonexistent workflow returns error",
			setup: func(r *Registry) {},
			operation: func(r *Registry) error {
				_, err := r.Versions("nonexistent")
				return err
			},
			wantErr: ErrWorkflowNotFound,
		},
		{
			name: "workflow def with version returns copy",
			setup: func(r *Registry) {},
			check: func(t *testing.T, r *Registry) {
				original := createTestWorkflow("test")
				v2 := original.WithVersion("v2")
				if original.Version() == v2.Version() {
					t.Error("WithVersion should return a new def with different version")
				}
				if original.Name() != v2.Name() {
					t.Error("WithVersion should preserve workflow name")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			tt.setup(r)

			if tt.operation != nil {
				err := tt.operation(r)
				if tt.wantErr != nil {
					if !errors.Is(err, tt.wantErr) {
						t.Errorf("expected error %v, got %v", tt.wantErr, err)
					}
					return
				}
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}

			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r := NewRegistry()
	done := make(chan bool)

	// Concurrent writers registering different versions
	for i := 0; i < 10; i++ {
		go func(i int) {
			for j := 0; j < 100; j++ {
				r.Register(createTestWorkflowWithVersion("workflow", "v1"))
			}
			done <- true
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = r.Get("workflow")
				_, _ = r.GetVersion("workflow", "v1")
				_ = r.Names()
				_ = r.Count()
				_, _ = r.Versions("workflow")
				_, _ = r.LatestVersion("workflow")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Should still be valid
	if r.Count() != 1 {
		t.Errorf("expected 1 workflow after concurrent access, got %d", r.Count())
	}
}

func TestWorkflowDef_WithVersion(t *testing.T) {
	tests := []struct {
		name            string
		originalVersion string
		newVersion      string
	}{
		{
			name:            "change from default to v2",
			originalVersion: "1",
			newVersion:      "v2",
		},
		{
			name:            "semver format",
			originalVersion: "1",
			newVersion:      "1.2.3",
		},
		{
			name:            "date format",
			originalVersion: "1",
			newVersion:      "2024-01-15",
		},
		{
			name:            "empty string",
			originalVersion: "1",
			newVersion:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := createTestWorkflow("test")

			// Verify default version
			if original.Version() != "1" {
				t.Errorf("expected default version to be '1', got %q", original.Version())
			}

			// Create versioned copy
			versioned := original.WithVersion(tt.newVersion)

			// Original should be unchanged
			if original.Version() != "1" {
				t.Errorf("original version changed to %q", original.Version())
			}

			// New version should match
			if versioned.Version() != tt.newVersion {
				t.Errorf("expected version %q, got %q", tt.newVersion, versioned.Version())
			}

			// Name should be preserved
			if versioned.Name() != original.Name() {
				t.Errorf("name changed from %q to %q", original.Name(), versioned.Name())
			}

			// Steps should be preserved (same slice)
			if len(versioned.Steps()) != len(original.Steps()) {
				t.Errorf("steps count changed")
			}
		})
	}
}
