// Package authz implements wolfCI's Jenkins-style role-based
// authorization matrix.
//
// Three roles are defined by the project (decision recorded under
// "## Phase 3" in PLAN.md):
//
//   - admin     full access (wildcard "*" permission)
//   - developer read jobs, trigger builds, read and cancel builds,
//     read nodes
//   - viewer    read jobs, read builds, read nodes
//
// There is no anonymous role: every action requires an authenticated
// user. Users absent from the Users map are denied. Users mapped to
// a role that is absent from the Roles map are also denied (no
// implicit grant).
//
// The matrix is persisted as YAML at config-files/auth/matrix.yaml.
package authz

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Permission identifies a single authorizable action. Permissions
// are namespaced dot-separated strings to keep room for future
// resources without polluting an enum.
type Permission string

const (
	JobsRead       Permission = "jobs.read"
	JobsBuild      Permission = "jobs.build"
	JobsConfigure  Permission = "jobs.configure"
	BuildsRead     Permission = "builds.read"
	BuildsCancel   Permission = "builds.cancel"
	NodesRead      Permission = "nodes.read"
	NodesConfigure Permission = "nodes.configure"

	// WildcardPermission grants every permission; used by the admin
	// role. Not assignable as the subject of an Allow query.
	WildcardPermission Permission = "*"
)

// Role identifies a permission bundle. wolfCI ships with three.
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// Matrix is the on-disk authorization model: a mapping from users
// to roles, paired with a mapping from roles to permissions.
type Matrix struct {
	Users map[string]Role        `yaml:"users"`
	Roles map[Role][]Permission  `yaml:"roles"`
}

// DefaultMatrix returns the v1 defaults: an empty Users map and a
// Roles map prepopulated with admin/developer/viewer.
func DefaultMatrix() *Matrix {
	return &Matrix{
		Users: map[string]Role{},
		Roles: map[Role][]Permission{
			RoleAdmin: {WildcardPermission},
			RoleDeveloper: {
				JobsRead,
				JobsBuild,
				BuildsRead,
				BuildsCancel,
				NodesRead,
			},
			RoleViewer: {
				JobsRead,
				BuildsRead,
				NodesRead,
			},
		},
	}
}

// Allow returns true iff user is assigned a role that grants p
// (directly or via WildcardPermission). Unknown users and users
// mapped to undefined roles return false.
func (m *Matrix) Allow(user string, p Permission) bool {
	if user == "" {
		return false
	}
	role, ok := m.Users[user]
	if !ok {
		return false
	}
	perms, ok := m.Roles[role]
	if !ok {
		return false
	}
	for _, granted := range perms {
		if granted == WildcardPermission || granted == p {
			return true
		}
	}
	return false
}

// LoadMatrix reads a Matrix from path.
func LoadMatrix(path string) (*Matrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("authz.LoadMatrix: %w", err)
	}
	m := &Matrix{}
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("authz.LoadMatrix: parse %s: %w", path, err)
	}
	// Normalize: nil maps in YAML decode to nil; downstream Allow
	// is safe with nil maps but other callers may want to mutate.
	if m.Users == nil {
		m.Users = map[string]Role{}
	}
	if m.Roles == nil {
		m.Roles = map[Role][]Permission{}
	}
	return m, nil
}

// Save writes the Matrix to path, creating intermediate
// directories.
func (m *Matrix) Save(path string) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("authz.Matrix.Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("authz.Matrix.Save: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("authz.Matrix.Save: write: %w", err)
	}
	return nil
}
