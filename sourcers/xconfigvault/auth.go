package xconfigvault

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

// AuthMethod is the interface for Vault authentication methods.
type AuthMethod interface {
	// Login authenticates with Vault and sets the client token.
	Login(ctx context.Context, client *vault.Client) error
	// Name returns the authentication method name for logging.
	Name() string
}

// TokenAuth uses a pre-existing token for authentication.
type TokenAuth struct {
	Token string
}

// WithToken creates a TokenAuth with the given token.
func WithToken(token string) AuthMethod {
	return &TokenAuth{Token: token}
}

func (a *TokenAuth) Login(ctx context.Context, client *vault.Client) error {
	if a.Token == "" {
		return fmt.Errorf("%w: token is empty", ErrAuthFailed)
	}
	if err := client.SetToken(a.Token); err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}
	return nil
}

func (a *TokenAuth) Name() string {
	return "token"
}

// AppRoleAuth uses AppRole authentication.
type AppRoleAuth struct {
	RoleID    string
	SecretID  string
	MountPath string // defaults to "approle"
}

// WithAppRole creates an AppRoleAuth with the given credentials.
func WithAppRole(roleID, secretID string) AuthMethod {
	return &AppRoleAuth{
		RoleID:   roleID,
		SecretID: secretID,
	}
}

// WithAppRolePath creates an AppRoleAuth with a custom mount path.
func WithAppRolePath(roleID, secretID, mountPath string) AuthMethod {
	return &AppRoleAuth{
		RoleID:    roleID,
		SecretID:  secretID,
		MountPath: mountPath,
	}
}

func (a *AppRoleAuth) Login(ctx context.Context, client *vault.Client) error {
	mountPath := a.MountPath
	if mountPath == "" {
		mountPath = "approle"
	}

	resp, err := client.Auth.AppRoleLogin(ctx, schema.AppRoleLoginRequest{
		RoleId:   a.RoleID,
		SecretId: a.SecretID,
	}, vault.WithMountPath(mountPath))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	if err := client.SetToken(resp.Auth.ClientToken); err != nil {
		return fmt.Errorf("%w: failed to set token: %v", ErrAuthFailed, err)
	}

	return nil
}

func (a *AppRoleAuth) Name() string {
	return "approle"
}

// KubernetesAuth uses Kubernetes service account authentication.
type KubernetesAuth struct {
	Role      string
	JWTPath   string // defaults to /var/run/secrets/kubernetes.io/serviceaccount/token
	MountPath string // defaults to "kubernetes"
}

// WithKubernetes creates a KubernetesAuth with the given role.
func WithKubernetes(role string) AuthMethod {
	return &KubernetesAuth{Role: role}
}

// WithKubernetesPath creates a KubernetesAuth with custom paths.
func WithKubernetesPath(role, jwtPath, mountPath string) AuthMethod {
	return &KubernetesAuth{
		Role:      role,
		JWTPath:   jwtPath,
		MountPath: mountPath,
	}
}

func (a *KubernetesAuth) Login(ctx context.Context, client *vault.Client) error {
	jwtPath := a.JWTPath
	if jwtPath == "" {
		jwtPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}

	jwt, err := os.ReadFile(jwtPath)
	if err != nil {
		return fmt.Errorf("%w: failed to read JWT token: %v", ErrAuthFailed, err)
	}

	mountPath := a.MountPath
	if mountPath == "" {
		mountPath = "kubernetes"
	}

	resp, err := client.Auth.KubernetesLogin(ctx, schema.KubernetesLoginRequest{
		Role: a.Role,
		Jwt:  string(jwt),
	}, vault.WithMountPath(mountPath))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	if err := client.SetToken(resp.Auth.ClientToken); err != nil {
		return fmt.Errorf("%w: failed to set token: %v", ErrAuthFailed, err)
	}

	return nil
}

func (a *KubernetesAuth) Name() string {
	return "kubernetes"
}

// UserPassAuth uses username/password authentication.
type UserPassAuth struct {
	Username  string
	Password  string
	MountPath string // defaults to "userpass"
}

// WithUserPass creates a UserPassAuth with the given credentials.
func WithUserPass(username, password string) AuthMethod {
	return &UserPassAuth{
		Username: username,
		Password: password,
	}
}

// WithUserPassPath creates a UserPassAuth with a custom mount path.
func WithUserPassPath(username, password, mountPath string) AuthMethod {
	return &UserPassAuth{
		Username:  username,
		Password:  password,
		MountPath: mountPath,
	}
}

func (a *UserPassAuth) Login(ctx context.Context, client *vault.Client) error {
	mountPath := a.MountPath
	if mountPath == "" {
		mountPath = "userpass"
	}

	resp, err := client.Auth.UserpassLogin(ctx, a.Username, schema.UserpassLoginRequest{
		Password: a.Password,
	}, vault.WithMountPath(mountPath))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	if err := client.SetToken(resp.Auth.ClientToken); err != nil {
		return fmt.Errorf("%w: failed to set token: %v", ErrAuthFailed, err)
	}

	return nil
}

func (a *UserPassAuth) Name() string {
	return "userpass"
}

// LDAPAuth uses LDAP authentication.
type LDAPAuth struct {
	Username  string
	Password  string
	MountPath string // defaults to "ldap"
}

// WithLDAP creates an LDAPAuth with the given credentials.
func WithLDAP(username, password string) AuthMethod {
	return &LDAPAuth{
		Username: username,
		Password: password,
	}
}

// WithLDAPPath creates an LDAPAuth with a custom mount path.
func WithLDAPPath(username, password, mountPath string) AuthMethod {
	return &LDAPAuth{
		Username:  username,
		Password:  password,
		MountPath: mountPath,
	}
}

func (a *LDAPAuth) Login(ctx context.Context, client *vault.Client) error {
	mountPath := a.MountPath
	if mountPath == "" {
		mountPath = "ldap"
	}

	resp, err := client.Auth.LdapLogin(ctx, a.Username, schema.LdapLoginRequest{
		Password: a.Password,
	}, vault.WithMountPath(mountPath))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	if err := client.SetToken(resp.Auth.ClientToken); err != nil {
		return fmt.Errorf("%w: failed to set token: %v", ErrAuthFailed, err)
	}

	return nil
}

func (a *LDAPAuth) Name() string {
	return "ldap"
}
