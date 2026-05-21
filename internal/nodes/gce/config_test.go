package gce_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/nodes/gce"
)

// TestConfig_RoundTrip exercises Save and LoadConfig against
// the locked-in Phase 5 decision: GCE credentials come from a
// service_account_key path in the config file. No reliance on
// Application Default Credentials.
func TestConfig_RoundTrip(t *testing.T) {
	original := &gce.Config{
		ProjectID:         "wolfci-ci",
		Zone:              "us-central1-a",
		MachineType:       "e2-medium",
		ServiceAccountKey: "/etc/wolfci/gce-sa.json",
		Image:             "projects/debian-cloud/global/images/family/debian-12",
		Network:           "default",
		Labels:            []string{"linux", "x86_64"},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nodes", "gce.yaml")
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := gce.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("Config round-trip mismatch.\noriginal: %+v\nloaded:   %+v", original, loaded)
	}
}

// TestLoadConfig_RejectsMissingRequiredFields ensures the
// loader fails fast on a config that is missing any of the
// fields needed to actually call GCE.
func TestLoadConfig_RejectsMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"empty", ""},
		{"missing project_id", "zone: z\nmachine_type: m\nservice_account_key: /k\nimage: i\n"},
		{"missing zone", "project_id: p\nmachine_type: m\nservice_account_key: /k\nimage: i\n"},
		{"missing service_account_key", "project_id: p\nzone: z\nmachine_type: m\nimage: i\n"},
		{"missing image", "project_id: p\nzone: z\nmachine_type: m\nservice_account_key: /k\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gce.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := gce.LoadConfig(path); err == nil {
				t.Errorf("expected error for %q, got nil", tc.name)
			}
		})
	}
}
