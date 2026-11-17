package projectcfg

import (
	"strings"
	"testing"
)

func TestDevOpenSettings(t *testing.T) {
	var missing *Config
	if got := missing.EffectiveDev().Open.Target; got != "frontend" {
		t.Fatalf("default target = %q", got)
	}
	cfg, err := Load(writeConfig(t, "dev:\n  open:\n    target: backend\n    backend_url: https://127.0.0.1:8443/admin\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EffectiveDev().Open; got.Target != "backend" || got.BackendURL != "https://127.0.0.1:8443/admin" {
		t.Fatalf("open config = %+v", got)
	}
	for _, body := range []string{
		"target: other", "backend_url: /relative", "backend_url: ftp://localhost", "backend_url: https://user:secret-sentinel@localhost", "backend_url: http://[secret-sentinel",
	} {
		_, err := Load(writeConfig(t, "dev:\n  open:\n    "+body+"\n"))
		if err == nil || !strings.Contains(err.Error(), "dev.open.") || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatalf("validation error = %v", err)
		}
	}
}
