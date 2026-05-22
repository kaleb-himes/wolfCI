package server_test

/* Gates PLAN.md task 11.1: cmd/wolfci reads its bootstrap config
 * from config-files/server.yaml. ServerConfig is the typed Go
 * shape of that file; LoadServerConfig parses + validates.
 */

import (
    "os"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/server"
)

func TestServerConfig_Roundtrip(t *testing.T) {
    original := &server.ServerConfig{
        ListenAddr:           "127.0.0.1:8443",
        Cert:                 "/etc/wolfci/server.crt",
        Key:                  "/etc/wolfci/server.key",
        CACert:               "/etc/wolfci/ca.crt",
        WorkDir:              "/var/lib/wolfci",
        ShutdownDrainTimeout: "45s",
        PluginDir:            "/etc/wolfci/plugins",
        GCEConfig:            "/etc/wolfci/gce.yaml",
    }

    dir := t.TempDir()
    path := filepath.Join(dir, "wolfci", "server.yaml")
    if err := original.Save(path); err != nil {
        t.Fatalf("Save: %v", err)
    }
    loaded, err := server.LoadServerConfig(path)
    if err != nil {
        t.Fatalf("LoadServerConfig: %v", err)
    }
    if !reflect.DeepEqual(original, loaded) {
        t.Fatalf("round-trip mismatch.\noriginal: %+v\nloaded:   %+v", original, loaded)
    }
}

func TestServerConfig_Defaults(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "server.yaml")
    yaml := "listen_addr: 0.0.0.0:8443\n" +
        "cert: /c\n" +
        "key: /k\n" +
        "ca_cert: /ca\n" +
        "work_dir: /w\n"
    if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }
    cfg, err := server.LoadServerConfig(path)
    if err != nil {
        t.Fatalf("LoadServerConfig: %v", err)
    }
    if cfg.PluginDir != "plugins/" {
        t.Errorf("default PluginDir = %q, want plugins/", cfg.PluginDir)
    }
    if cfg.GCEConfig != "" {
        t.Errorf("default GCEConfig = %q, want empty", cfg.GCEConfig)
    }
    d, err := cfg.DrainTimeout()
    if err != nil {
        t.Fatalf("DrainTimeout: %v", err)
    }
    if d != 30*time.Second {
        t.Errorf("default DrainTimeout = %v, want 30s", d)
    }
}

func TestServerConfig_RejectsMissingRequiredFields(t *testing.T) {
    base := map[string]string{
        "listen_addr": "0.0.0.0:8443",
        "cert":        "/c",
        "key":         "/k",
        "ca_cert":     "/ca",
        "work_dir":    "/w",
    }
    /* Each required key is removed in turn; the loader must
     * reject the result.
     */
    for required := range base {
        t.Run("missing_"+required, func(t *testing.T) {
            buf := ""
            for k, v := range base {
                if k == required {
                    continue
                }
                buf += k + ": " + v + "\n"
            }
            dir := t.TempDir()
            path := filepath.Join(dir, "server.yaml")
            if err := os.WriteFile(path, []byte(buf), 0o644); err != nil {
                t.Fatalf("WriteFile: %v", err)
            }
            if _, err := server.LoadServerConfig(path); err == nil {
                t.Errorf("expected error when %q is missing", required)
            }
        })
    }
    /* Empty file: every required field is absent. */
    t.Run("empty", func(t *testing.T) {
        dir := t.TempDir()
        path := filepath.Join(dir, "server.yaml")
        if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
            t.Fatalf("WriteFile: %v", err)
        }
        if _, err := server.LoadServerConfig(path); err == nil {
            t.Error("expected error for empty config, got nil")
        }
    })
}

func TestServerConfig_RejectsBadDuration(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "server.yaml")
    yaml := "listen_addr: 0.0.0.0:8443\n" +
        "cert: /c\n" +
        "key: /k\n" +
        "ca_cert: /ca\n" +
        "work_dir: /w\n" +
        "shutdown_drain_timeout: notaduration\n"
    if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }
    if _, err := server.LoadServerConfig(path); err == nil {
        t.Error("expected error for bad shutdown_drain_timeout, got nil")
    }
}
