package authz_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/authz"
)

// TestMatrix_DefaultRoles is the gating test for PLAN.md tasks
// 3.5 and 3.6. It pins the default permission set for each of
// the three Phase 3 roles (admin, developer, viewer; no
// anonymous), and checks that an unknown user is denied.
func TestMatrix_DefaultRoles(t *testing.T) {
	m := authz.DefaultMatrix()
	m.Users["alice"] = authz.RoleAdmin
	m.Users["bob"] = authz.RoleDeveloper
	m.Users["carol"] = authz.RoleViewer

	cases := []struct {
		user string
		perm authz.Permission
		want bool
	}{
		// admin: everything
		{"alice", authz.JobsRead, true},
		{"alice", authz.JobsBuild, true},
		{"alice", authz.JobsConfigure, true},
		{"alice", authz.BuildsRead, true},
		{"alice", authz.BuildsCancel, true},
		{"alice", authz.NodesRead, true},
		{"alice", authz.NodesConfigure, true},

		// developer: read + build, no configure
		{"bob", authz.JobsRead, true},
		{"bob", authz.JobsBuild, true},
		{"bob", authz.BuildsRead, true},
		{"bob", authz.BuildsCancel, true},
		{"bob", authz.NodesRead, true},
		{"bob", authz.JobsConfigure, false},
		{"bob", authz.NodesConfigure, false},

		// viewer: read-only
		{"carol", authz.JobsRead, true},
		{"carol", authz.BuildsRead, true},
		{"carol", authz.NodesRead, true},
		{"carol", authz.JobsBuild, false},
		{"carol", authz.BuildsCancel, false},
		{"carol", authz.JobsConfigure, false},
		{"carol", authz.NodesConfigure, false},

		// unknown user: denied (no anonymous role)
		{"mallory", authz.JobsRead, false},
		{"", authz.JobsRead, false},
	}
	for _, tc := range cases {
		if got := m.Allow(tc.user, tc.perm); got != tc.want {
			t.Errorf("Allow(%q, %q) = %v, want %v", tc.user, tc.perm, got, tc.want)
		}
	}
}

// TestMatrix_Roundtrip exercises Save and LoadMatrix to ensure
// the YAML form round-trips faithfully.
func TestMatrix_Roundtrip(t *testing.T) {
	original := authz.DefaultMatrix()
	original.Users["alice"] = authz.RoleAdmin
	original.Users["bob"] = authz.RoleDeveloper
	original.Users["carol"] = authz.RoleViewer

	dir := t.TempDir()
	path := filepath.Join(dir, "auth", "matrix.yaml")
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := authz.LoadMatrix(path)
	if err != nil {
		t.Fatalf("LoadMatrix: %v", err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("Matrix round-trip mismatch.\noriginal: %+v\nloaded:   %+v", original, loaded)
	}
}

// TestMatrix_UnknownRoleDenies guards against a user mapped to a
// role that does not exist in the role table; the result must be
// deny, not panic.
func TestMatrix_UnknownRoleDenies(t *testing.T) {
	m := authz.DefaultMatrix()
	m.Users["weirdo"] = authz.Role("ghost-role")
	if m.Allow("weirdo", authz.JobsRead) {
		t.Fatal("Allow with unknown role: true, want false")
	}
}
