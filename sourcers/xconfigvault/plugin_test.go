package xconfigvault

import (
	"reflect"
	"testing"
)

// TestApplySecretsExpansion verifies that VaultPlugin.applySecrets grows
// slice-of-struct fields by index and creates map entries by key based on
// the secret keys — mirroring the env plugin's behaviour.
//
// applySecrets does not touch the Vault client, so we can exercise the full
// expansion path with a synthetic secret map.
func TestApplySecretsExpansion(t *testing.T) {
	type Server struct {
		Host string
		Pwd  string `vault:"true"`
	}

	type ServerCfg struct {
		Host     string
		Password string `vault:"true"`
	}

	type Config struct {
		Items   []Server
		PtrSrv  []*Server
		Servers map[string]ServerCfg
		Tokens  map[string]*ServerCfg
	}

	cfg := &Config{}
	plugin := &VaultPlugin{conf: cfg}

	secrets := map[string]string{
		// slice of struct: ITEMS_<N>_PWD comes from vault, ITEMS_<N>_HOST is just
		// in vault here for the sake of expansion (vault tag is on Pwd only — Host
		// stays zero on purpose).
		"ITEMS_0_PWD":   "pwd-zero",
		"ITEMS_2_PWD":   "pwd-two",
		"PTR_SRV_0_PWD": "pp-zero",

		// map of struct
		"SERVERS_PRIMARY_PASSWORD": "primary-secret",
		"SERVERS_BACKUP_PASSWORD":  "backup-secret",

		// map of *struct
		"TOKENS_A_PASSWORD": "tok-a",
	}

	if err := plugin.applySecrets(secrets); err != nil {
		t.Fatalf("applySecrets failed: %v", err)
	}

	if got, want := len(cfg.Items), 3; got != want {
		t.Fatalf("Items length: got %d, want %d", got, want)
	}
	if cfg.Items[0].Pwd != "pwd-zero" {
		t.Errorf("Items[0].Pwd = %q, want %q", cfg.Items[0].Pwd, "pwd-zero")
	}
	if cfg.Items[1].Pwd != "" {
		t.Errorf("Items[1].Pwd should be empty (no secret), got %q", cfg.Items[1].Pwd)
	}
	if cfg.Items[2].Pwd != "pwd-two" {
		t.Errorf("Items[2].Pwd = %q, want %q", cfg.Items[2].Pwd, "pwd-two")
	}

	if got, want := len(cfg.PtrSrv), 1; got != want {
		t.Fatalf("PtrSrv length: got %d, want %d", got, want)
	}
	if cfg.PtrSrv[0] == nil {
		t.Fatalf("PtrSrv[0] is nil — expected &Server{} allocation")
	}
	if cfg.PtrSrv[0].Pwd != "pp-zero" {
		t.Errorf("PtrSrv[0].Pwd = %q, want %q", cfg.PtrSrv[0].Pwd, "pp-zero")
	}

	expectServers := map[string]ServerCfg{
		"PRIMARY": {Password: "primary-secret"},
		"BACKUP":  {Password: "backup-secret"},
	}
	if !reflect.DeepEqual(cfg.Servers, expectServers) {
		t.Errorf("Servers mismatch:\n want: %+v\n got:  %+v", expectServers, cfg.Servers)
	}

	if got := cfg.Tokens["A"]; got == nil || got.Password != "tok-a" {
		t.Errorf("Tokens[A] = %+v, want non-nil with Password=%q", got, "tok-a")
	}
}

// TestApplySecretsRespectsExisting verifies that values already present in
// the conf (e.g. populated by a YAML loader earlier in the chain) survive
// the expansion pass — only vault-tagged fields are touched.
func TestApplySecretsRespectsExisting(t *testing.T) {
	type Server struct {
		Host string
		Pwd  string `vault:"true"`
	}

	type Config struct {
		Items []Server
	}

	cfg := &Config{
		Items: []Server{
			{Host: "host-a"},
			{Host: "host-b", Pwd: "old-pwd"},
		},
	}
	plugin := &VaultPlugin{conf: cfg}

	secrets := map[string]string{
		"ITEMS_1_PWD": "new-pwd",
	}

	if err := plugin.applySecrets(secrets); err != nil {
		t.Fatalf("applySecrets failed: %v", err)
	}

	if cfg.Items[0].Host != "host-a" || cfg.Items[0].Pwd != "" {
		t.Errorf("Items[0] mutated unexpectedly: %+v", cfg.Items[0])
	}
	if cfg.Items[1].Host != "host-b" || cfg.Items[1].Pwd != "new-pwd" {
		t.Errorf("Items[1] = %+v, want {host-b new-pwd}", cfg.Items[1])
	}
}

// TestApplySecretsWithEnvPrefix verifies that an explicit env prefix is
// propagated to ExpandContainersFromKeys, so vault keys like
// MYAPP_ITEMS_0_PWD correctly grow Items and apply values. Without the
// prefix the vault plugin computes container env names without "MYAPP_"
// and the keys never match.
func TestApplySecretsWithEnvPrefix(t *testing.T) {
	type Server struct {
		Host string
		Pwd  string `vault:"true"`
	}
	type Config struct {
		Items []Server
	}

	cfg := &Config{}
	plugin := &VaultPlugin{conf: cfg, envPrefix: "MYAPP"}

	secrets := map[string]string{
		"MYAPP_ITEMS_0_PWD": "first",
		"MYAPP_ITEMS_1_PWD": "second",
	}

	if err := plugin.applySecrets(secrets); err != nil {
		t.Fatalf("applySecrets failed: %v", err)
	}

	if got, want := len(cfg.Items), 2; got != want {
		t.Fatalf("Items length: got %d, want %d", got, want)
	}
	if cfg.Items[0].Pwd != "first" || cfg.Items[1].Pwd != "second" {
		t.Errorf("Items = %+v, want [{first} {second}]", cfg.Items)
	}
}

// TestApplySecretsFieldEnvTag verifies that an env tag inside a slice/map
// element acts as a per-segment override — the surrounding index/key stays
// in the lookup key so different elements don't collide.
func TestApplySecretsFieldEnvTag(t *testing.T) {
	type Server struct {
		Pwd string `vault:"true" env:"P"`
	}
	type Config struct {
		Items []Server
	}

	cfg := &Config{}
	plugin := &VaultPlugin{conf: cfg}

	secrets := map[string]string{
		"ITEMS_0_P": "first",
		"ITEMS_1_P": "second",
	}

	if err := plugin.applySecrets(secrets); err != nil {
		t.Fatalf("applySecrets failed: %v", err)
	}

	if got, want := len(cfg.Items), 2; got != want {
		t.Fatalf("Items length: got %d, want %d", got, want)
	}
	if cfg.Items[0].Pwd != "first" || cfg.Items[1].Pwd != "second" {
		t.Errorf("Items = %+v, want [{first} {second}]", cfg.Items)
	}
}
