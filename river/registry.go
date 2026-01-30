// Package river provides integration between the Skene workflow engine and
// River job queue for durable workflow execution.
package river

import (
	"errors"
	"fmt"
	"sync"

	"github.com/lirancohen/skene/workflow"
)

// ErrWorkflowNotFound is returned when a workflow is not registered.
var ErrWorkflowNotFound = errors.New("workflow not found")

// ErrVersionNotFound is returned when a specific version is not registered.
var ErrVersionNotFound = errors.New("version not found")

// versionedWorkflow holds all versions of a workflow and tracks the latest.
type versionedWorkflow struct {
	versions map[string]*workflow.WorkflowDef
	latest   string
}

// Registry stores workflow definitions for lookup during job execution.
// It supports multiple versions of each workflow and tracks which version
// is "latest" for new workflow starts.
// It is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	workflows map[string]*versionedWorkflow
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{
		workflows: make(map[string]*versionedWorkflow),
	}
}

// Register adds a workflow definition to the registry.
// The workflow's version is taken from the definition.
// If this is the first version registered, it becomes the latest.
// Otherwise, the latest version is unchanged (use SetLatest to change it).
func (r *Registry) Register(def *workflow.WorkflowDef) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := def.Name()
	version := def.Version()

	vw, exists := r.workflows[name]
	if !exists {
		vw = &versionedWorkflow{
			versions: make(map[string]*workflow.WorkflowDef),
		}
		r.workflows[name] = vw
	}

	vw.versions[version] = def

	// If this is the first version, make it the latest
	if vw.latest == "" {
		vw.latest = version
	}
}

// Get retrieves the latest version of a workflow definition by name.
// Returns ErrWorkflowNotFound if the workflow is not registered.
func (r *Registry) Get(name string) (*workflow.WorkflowDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	vw, ok := r.workflows[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	def, ok := vw.versions[vw.latest]
	if !ok {
		return nil, fmt.Errorf("%w: %s (latest=%s)", ErrVersionNotFound, name, vw.latest)
	}
	return def, nil
}

// GetVersion retrieves a specific version of a workflow definition.
// Returns ErrWorkflowNotFound if the workflow is not registered.
// Returns ErrVersionNotFound if the specific version is not registered.
func (r *Registry) GetVersion(name, version string) (*workflow.WorkflowDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	vw, ok := r.workflows[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	def, ok := vw.versions[version]
	if !ok {
		return nil, fmt.Errorf("%w: %s version %s", ErrVersionNotFound, name, version)
	}
	return def, nil
}

// SetLatest marks a specific version as the latest for a workflow.
// New workflow starts will use this version.
// Returns ErrWorkflowNotFound if the workflow is not registered.
// Returns ErrVersionNotFound if the specific version is not registered.
func (r *Registry) SetLatest(name, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	vw, ok := r.workflows[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	if _, ok := vw.versions[version]; !ok {
		return fmt.Errorf("%w: %s version %s", ErrVersionNotFound, name, version)
	}

	vw.latest = version
	return nil
}

// LatestVersion returns the latest version string for a workflow.
// Returns ErrWorkflowNotFound if the workflow is not registered.
func (r *Registry) LatestVersion(name string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	vw, ok := r.workflows[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	return vw.latest, nil
}

// Versions returns all registered versions for a workflow.
// Returns ErrWorkflowNotFound if the workflow is not registered.
func (r *Registry) Versions(name string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	vw, ok := r.workflows[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkflowNotFound, name)
	}

	versions := make([]string, 0, len(vw.versions))
	for v := range vw.versions {
		versions = append(versions, v)
	}
	return versions, nil
}

// Names returns the names of all registered workflows.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.workflows))
	for name := range r.workflows {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered workflows (unique names, not versions).
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.workflows)
}
