package config

import (
	"strings"
	"testing"
)

func TestAPIConfigDefaults(t *testing.T) {
	a := APIConfig{}
	a.ApplyDefaults()
	if a.BindAddr != "127.0.0.1:7766" {
		t.Errorf("BindAddr default: got %q", a.BindAddr)
	}
	if a.EventBuffer != 1000 {
		t.Errorf("EventBuffer default: got %d", a.EventBuffer)
	}
	if a.MaxConnections != 4 {
		t.Errorf("MaxConnections default: got %d", a.MaxConnections)
	}
	if a.NodeName != "gnb-node" {
		t.Errorf("NodeName default: got %q", a.NodeName)
	}
}

func TestAPIConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     APIConfig
		wantErr string
	}{
		{"disabled passes", APIConfig{Enabled: false}, ""},
		{"short token", APIConfig{Enabled: true, AuthToken: "abc", BindAddr: "127.0.0.1:1"}, "auth_token"},
		{"cert without key", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "127.0.0.1:1", TLSCertFile: "c.pem"}, "tls_cert_file"},
		{"bad bind", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "no-colon"}, "bind_addr"},
		{"ok plain", APIConfig{Enabled: true, AuthToken: strings.Repeat("x", 32), BindAddr: "127.0.0.1:7766"}, ""},
	}
	for _, c := range cases {
		err := c.cfg.Validate()
		if c.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.wantErr, err)
		}
	}
}
