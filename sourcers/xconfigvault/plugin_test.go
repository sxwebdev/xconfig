package xconfigvault

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var errMissingScheme = errors.New("missing scheme")

type rejectingEndpoint string

func (*rejectingEndpoint) UnmarshalText([]byte) error {
	return errMissingScheme
}

type mutableEndpoint struct {
	Host string
}

func (e *mutableEndpoint) UnmarshalText(text []byte) error {
	e.Host = string(text)
	if strings.HasPrefix(e.Host, "invalid") {
		return errMissingScheme
	}
	return nil
}

type pointerEndpoint string

func (e *pointerEndpoint) UnmarshalText(text []byte) error {
	*e = pointerEndpoint(text)
	return nil
}

// TestApplySecretsExpansion verifies that VaultPlugin.applySecretsLocked grows
// slice-of-struct fields by index and creates map entries by key based on
// the secret keys — mirroring the env plugin's behaviour.
//
// applySecretsLocked does not touch the Vault client, so we can exercise the
// full expansion path with a synthetic secret map.
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

	if err := plugin.applySecretsLocked(secrets); err != nil {
		t.Fatalf("applySecretsLocked failed: %v", err)
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

func TestApplySecretsRedactsInvalidValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Port int `vault:"true"`
	}

	const secret = "private-endpoint-value"
	plugin := &VaultPlugin{conf: &Config{}}
	err := plugin.applySecretsLocked(map[string]string{"PORT": secret})
	if err == nil {
		t.Fatal("applySecretsLocked() error = nil, want invalid integer error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("applySecretsLocked() leaked secret in error: %v", err)
	}
	if !errors.Is(err, strconv.ErrSyntax) {
		t.Fatalf("applySecretsLocked() error = %v, want strconv.ErrSyntax", err)
	}
	if !errors.Is(err, ErrInvalidSecretValue) {
		t.Fatalf("applySecretsLocked() error = %v, want ErrInvalidSecretValue", err)
	}
	var numberError *strconv.NumError
	if errors.As(err, &numberError) {
		t.Fatalf("errors.As exposed secret-bearing NumError: %+v", numberError)
	}
}

func TestApplySecretsRedactsEscapedInvalidValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Port int `vault:"true"`
	}
	const secret = "private\"\\value\nnext"
	plugin := &VaultPlugin{conf: &Config{}}
	err := plugin.applySecretsLocked(map[string]string{"PORT": secret})
	if err == nil {
		t.Fatal("applySecretsLocked() error = nil, want invalid integer error")
	}
	if strings.Contains(err.Error(), strconv.Quote(secret)) {
		t.Fatalf("applySecretsLocked() leaked quoted secret: %v", err)
	}
}

// TestApplySecretsAppliesEmptyVaultValue pins the empty-value semantic on the
// Parse path: a key present in Vault with an empty value is a real value that
// revokes the field, while a key absent from Vault leaves the fallback alone.
func TestApplySecretsAppliesEmptyVaultValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Password string `vault:"true"`
		Fallback string `vault:"true"`
	}

	cfg := &Config{Password: "old-password", Fallback: "from-env"}
	plugin := &VaultPlugin{conf: cfg}

	if err := plugin.applySecretsLocked(map[string]string{"PASSWORD": ""}); err != nil {
		t.Fatalf("applySecretsLocked() error = %v", err)
	}
	if cfg.Password != "" {
		t.Errorf("Password = %q, want revoked empty value", cfg.Password)
	}
	if cfg.Fallback != "from-env" {
		t.Errorf("Fallback = %q, want untouched from-env (key absent from Vault)", cfg.Fallback)
	}
}

// TestApplySecretsInvalidValueFailsDeterministically verifies that when several
// vault-tagged fields reject their value, Parse always aborts on the same key
// instead of whichever one Go's randomized map iteration happened to reach
// first. It also pins that an empty value a field type cannot accept is an
// error rather than a silent skip.
func TestApplySecretsInvalidValueFailsDeterministically(t *testing.T) {
	t.Parallel()

	type Config struct {
		Alpha int `vault:"true"`
		Beta  int `vault:"true"`
		Gamma int `vault:"true"`
	}

	secrets := map[string]string{
		"ALPHA": "",
		"BETA":  "private-beta",
		"GAMMA": "private-gamma",
	}

	for range 50 {
		plugin := &VaultPlugin{conf: &Config{}}
		err := plugin.applySecretsLocked(secrets)
		var valueErr *SecretValueError
		if !errors.As(err, &valueErr) {
			t.Fatalf("applySecretsLocked() error = %v, want *SecretValueError", err)
		}
		if valueErr.Key != "ALPHA" {
			t.Fatalf("aborted on key %q, want ALPHA (first key in sorted order)", valueErr.Key)
		}
	}
}

func TestRefreshPublishesValidSecretsAndWarnsForInvalidValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Port  int    `vault:"true"`
		Token string `vault:"true"`
	}

	const invalid = "private-invalid-port"
	config := &Config{Port: 8080, Token: "old-token"}
	plugin := &VaultPlugin{conf: config}

	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{
		"PORT":  invalid,
		"TOKEN": "new-token",
	})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Port != 8080 {
		t.Fatalf("invalid Port mutated config to %d", config.Port)
	}
	if config.Token != "new-token" {
		t.Fatalf("valid Token = %q, want new-token", config.Token)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Token" {
		t.Fatalf("changes = %+v, want Token", outcome.Changes)
	}
	if len(outcome.Warnings) != 1 || !errors.Is(outcome.Warnings[0], strconv.ErrSyntax) {
		t.Fatalf("warnings = %+v, want strconv.ErrSyntax", outcome.Warnings)
	}
	if strings.Contains(outcome.Warnings[0].Error(), invalid) {
		t.Fatalf("warning leaked secret: %v", outcome.Warnings[0])
	}
}

func TestRefreshPreservesVaultFieldsMissingFromResponse(t *testing.T) {
	t.Parallel()

	type Config struct {
		Extra string `vault:"true"`
		Port  int    `vault:"true"`
		Token string `vault:"true"`
	}
	config := &Config{Extra: "private-from-env", Port: 8443, Token: "old-token"}
	plugin := &VaultPlugin{conf: config}

	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{"TOKEN": "new-token"})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Extra != "private-from-env" || config.Port != 8443 {
		t.Fatalf("missing Vault keys zeroed config: %+v", config)
	}
	if config.Token != "new-token" {
		t.Fatalf("Token = %q, want new-token", config.Token)
	}
	if len(outcome.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none for missing keys", outcome.Warnings)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Token" {
		t.Fatalf("changes = %+v, want Token only", outcome.Changes)
	}
}

func TestRefreshAppliesEmptyVaultValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Password string `vault:"true"`
	}
	config := &Config{Password: "old-password"}
	plugin := &VaultPlugin{conf: config}
	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{"PASSWORD": ""})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Password != "" {
		t.Fatalf("Password = %q, want revoked empty value", config.Password)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Password" {
		t.Fatalf("changes = %+v, want Password", outcome.Changes)
	}
}

// TestRefreshEmptyValueRejectedByFieldTypeWarns is the refresh-side twin of
// TestApplySecretsInvalidValueFailsDeterministically: an empty value revokes a
// string field, while the same empty value on a field type that cannot accept
// it keeps the last-known-good value and surfaces a redacted warning instead of
// failing the whole refresh.
func TestRefreshEmptyValueRejectedByFieldTypeWarns(t *testing.T) {
	t.Parallel()

	type Config struct {
		Port  int    `vault:"true"`
		Token string `vault:"true"`
	}
	config := &Config{Port: 8080, Token: "old-token"}
	plugin := &VaultPlugin{conf: config}

	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{
		"PORT":  "",
		"TOKEN": "",
	})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Port != 8080 {
		t.Fatalf("Port = %d, want last-known-good 8080", config.Port)
	}
	if config.Token != "" {
		t.Fatalf("Token = %q, want revoked empty value", config.Token)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Token" {
		t.Fatalf("changes = %+v, want Token", outcome.Changes)
	}
	if len(outcome.Warnings) != 1 || !errors.Is(outcome.Warnings[0], ErrInvalidSecretValue) {
		t.Fatalf("warnings = %+v, want ErrInvalidSecretValue for Port", outcome.Warnings)
	}
}

func TestRefreshDetectsPointerTextUnmarshalerChange(t *testing.T) {
	t.Parallel()

	type Config struct {
		Endpoint *pointerEndpoint `vault:"true"`
	}
	endpoint := pointerEndpoint("old.example")
	config := &Config{Endpoint: &endpoint}
	plugin := &VaultPlugin{conf: config}
	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{"ENDPOINT": "new.example"})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Endpoint != &endpoint || endpoint != "new.example" {
		t.Fatalf("Endpoint identity/value = %p/%q, want %p/new.example", config.Endpoint, endpoint, &endpoint)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Endpoint" {
		t.Fatalf("changes = %+v, want Endpoint", outcome.Changes)
	}
}

func TestRefreshRejectedPointerValueRemainsLastKnownGood(t *testing.T) {
	t.Parallel()

	type Config struct {
		Endpoint *mutableEndpoint `vault:"true"`
		Token    string           `vault:"true"`
	}
	endpoint := &mutableEndpoint{Host: "old.example"}
	config := &Config{Endpoint: endpoint, Token: "old-token"}
	plugin := &VaultPlugin{conf: config}
	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{
		"ENDPOINT": "invalid-private-endpoint",
		"TOKEN":    "new-token",
	})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if config.Endpoint != endpoint || endpoint.Host != "old.example" {
		t.Fatalf("rejected endpoint mutated last-known-good value: %p/%q", config.Endpoint, endpoint.Host)
	}
	if config.Token != "new-token" {
		t.Fatalf("valid sibling Token = %q, want new-token", config.Token)
	}
	if len(outcome.Changes) != 1 || outcome.Changes[0].FieldName != "Token" {
		t.Fatalf("changes = %+v, want Token only", outcome.Changes)
	}
}

func TestSecretValueErrorPreservesCustomCauseAndRedactsValue(t *testing.T) {
	t.Parallel()

	type Config struct {
		Endpoint *rejectingEndpoint `vault:"true"`
	}
	endpoint := rejectingEndpoint("https://old.example")
	config := &Config{Endpoint: &endpoint}
	plugin := &VaultPlugin{conf: config}
	const secret = "private://new.example"

	outcome, err := plugin.applyRefreshSecrets(config, map[string]string{"ENDPOINT": secret})
	if err != nil {
		t.Fatalf("applyRefreshSecrets() error = %v", err)
	}
	if len(outcome.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one", outcome.Warnings)
	}
	warning := outcome.Warnings[0]
	if !errors.Is(warning, errMissingScheme) {
		t.Fatalf("warning = %v, want preserved missing-scheme cause", warning)
	}
	if !errors.Is(warning, ErrInvalidSecretValue) {
		t.Fatalf("warning = %v, want ErrInvalidSecretValue", warning)
	}
	// The custom error's own message is not printed: only strconv reasons are
	// known to be free of the rejected value, so anything else is redacted to
	// the generic reason and stays reachable through errors.Is only.
	if strings.Contains(warning.Error(), "missing scheme") {
		t.Fatalf("warning = %v, want unvetted cause message redacted", warning)
	}
	if !strings.Contains(warning.Error(), "Endpoint") || !strings.Contains(warning.Error(), "ENDPOINT") {
		t.Fatalf("warning = %v, want field name and key for diagnosis", warning)
	}
	if strings.Contains(warning.Error(), secret) {
		t.Fatalf("warning leaked secret: %v", warning)
	}
	if cause := errors.Unwrap(warning); cause != nil && strings.Contains(cause.Error(), secret) {
		t.Fatalf("unwrapped warning leaked secret: %v", cause)
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

	if err := plugin.applySecretsLocked(secrets); err != nil {
		t.Fatalf("applySecretsLocked failed: %v", err)
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

	if err := plugin.applySecretsLocked(secrets); err != nil {
		t.Fatalf("applySecretsLocked failed: %v", err)
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

	if err := plugin.applySecretsLocked(secrets); err != nil {
		t.Fatalf("applySecretsLocked failed: %v", err)
	}

	if got, want := len(cfg.Items), 2; got != want {
		t.Fatalf("Items length: got %d, want %d", got, want)
	}
	if cfg.Items[0].Pwd != "first" || cfg.Items[1].Pwd != "second" {
		t.Errorf("Items = %+v, want [{first} {second}]", cfg.Items)
	}
}
