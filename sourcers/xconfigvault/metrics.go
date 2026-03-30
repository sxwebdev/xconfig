package xconfigvault

import "time"

// EventType identifies the kind of operational event emitted by the vault client.
type EventType string

const (
	// EventAuthSuccess is emitted after successful initial authentication.
	EventAuthSuccess EventType = "auth_success"
	// EventAuthFailed is emitted when initial authentication fails.
	EventAuthFailed EventType = "auth_failed"
	// EventTokenRenewed is emitted after a successful token renew-self.
	EventTokenRenewed EventType = "token_renewed"
	// EventTokenRenewFailed is emitted when token renew-self fails.
	EventTokenRenewFailed EventType = "token_renew_failed"
	// EventReloginSuccess is emitted after a successful full re-login.
	EventReloginSuccess EventType = "relogin_success"
	// EventReloginFailed is emitted when full re-login fails.
	EventReloginFailed EventType = "relogin_failed"
	// EventVaultUnreachable is emitted when the vault server cannot be reached.
	EventVaultUnreachable EventType = "vault_unreachable"
	// EventSecretsFetched is emitted after successfully fetching secrets.
	EventSecretsFetched EventType = "secrets_fetched"
	// EventRetryAttempt is emitted on each retry attempt after a retryable error.
	EventRetryAttempt EventType = "retry_attempt"
)

// Event represents an operational event from the vault client.
type Event struct {
	Type      EventType
	Message   string
	Error     error
	Attempt   int // retry attempt number (1-based), 0 if not a retry
	Timestamp time.Time
}

// MetricsCallback receives operational events from the vault client.
// Implementations should be safe for concurrent use.
type MetricsCallback interface {
	OnEvent(event Event)
}

// MetricsFunc is an adapter to use a plain function as MetricsCallback.
type MetricsFunc func(Event)

// OnEvent implements MetricsCallback.
func (f MetricsFunc) OnEvent(e Event) { f(e) }
