package api

import "net/http"

// Access levels.
type Access int

const (
	// Public routes need no session.
	Public Access = iota
	// SetupOnly routes work only while first-run setup is pending.
	SetupOnly
	// Authed routes need a valid session (and the CSRF token for unsafe
	// methods).
	Authed
	// StepUp routes additionally need a recent password+TOTP or passkey
	// proof (see StepUpWindow).
	StepUp
)

func (a Access) String() string {
	return [...]string{"public", "setup", "authed", "stepup"}[a]
}

// Route is one API endpoint.
type Route struct {
	Method  string
	Pattern string
	Access  Access
	// LocalOnly routes are only registered on the local listener.
	LocalOnly bool
	Handler   func(http.ResponseWriter, *http.Request, *reqCtx) error
}

// table is the single list of API endpoints. The security tests iterate it
// and compare it with a golden list, so every new endpoint must be reviewed.
func (s *Server) table() []Route {
	G, P, U, D := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete
	return []Route{
		// Public
		{G, "/api/health", Public, false, s.handleHealth},
		{G, "/api/meta", Public, false, s.handleMeta},
		{G, "/api/branding/{asset}", Public, false, s.handleBrandingAsset},

		// First-run setup (local listener only, before setup)
		{P, "/api/setup/begin", SetupOnly, true, s.handleSetupBegin},
		{P, "/api/setup/finish", SetupOnly, true, s.handleSetupFinish},

		// Authentication
		{P, "/api/auth/login", Public, false, s.handleLogin},
		{P, "/api/auth/login/mfa", Public, false, s.handleLoginMFA},
		{P, "/api/auth/passkey/begin", Public, false, s.handlePasskeyLoginBegin},
		{P, "/api/auth/passkey/finish", Public, false, s.handlePasskeyLoginFinish},
		{G, "/api/auth/session", Authed, false, s.handleSession},
		{P, "/api/auth/logout", Authed, false, s.handleLogout},
		{P, "/api/auth/reauth", Authed, false, s.handleReauth},

		// Account
		{G, "/api/account/passkeys", Authed, false, s.handlePasskeyList},
		{P, "/api/account/passkeys/begin", StepUp, false, s.handlePasskeyRegisterBegin},
		{P, "/api/account/passkeys/finish", StepUp, false, s.handlePasskeyRegisterFinish},
		{D, "/api/account/passkeys/{id}", StepUp, false, s.handlePasskeyDelete},
		{P, "/api/account/password", StepUp, false, s.handlePasswordChange},
		{P, "/api/account/recovery-codes", StepUp, false, s.handleRecoveryCodes},
		{P, "/api/account/totp/begin", StepUp, false, s.handleTOTPBegin},
		{P, "/api/account/totp/finish", StepUp, false, s.handleTOTPFinish},
		{P, "/api/account/sessions/revoke-others", Authed, false, s.handleRevokeOthers},

		// Hub (app launcher) and system widgets
		{G, "/api/hub", Authed, false, s.handleHub},
		{G, "/api/hub/icons/{id}", Authed, false, s.handleHubIcon},
		{G, "/api/system/stats", Authed, false, s.handleSystemStats},
		{U, "/api/routes/{id}/icon", Authed, false, s.handleRouteIconSet},
		{D, "/api/routes/{id}/icon", Authed, false, s.handleRouteIconDelete},

		// Overview, settings, audit
		{G, "/api/overview", Authed, false, s.handleOverview},
		{G, "/api/settings", Authed, false, s.handleSettingsGet},
		{U, "/api/settings", StepUp, false, s.handleSettingsPut},
		{G, "/api/audit", Authed, false, s.handleAudit},
		{U, "/api/branding", StepUp, false, s.handleBrandingPut},
		{U, "/api/branding/{asset}", StepUp, false, s.handleBrandingAssetPut},
		{D, "/api/branding/{asset}", StepUp, false, s.handleBrandingAssetDelete},
		{G, "/api/authelia/notification", Authed, false, s.handleAutheliaNotification},

		// DNS profiles
		{G, "/api/dns/providers", Authed, false, s.handleDNSProviders},
		{G, "/api/dns/profiles", Authed, false, s.handleDNSList},
		{P, "/api/dns/profiles", Authed, false, s.handleDNSCreate},
		{U, "/api/dns/profiles/{id}", StepUp, false, s.handleDNSUpdate},
		{D, "/api/dns/profiles/{id}", StepUp, false, s.handleDNSDelete},
		{P, "/api/dns/profiles/{id}/test", Authed, false, s.handleDNSTest},

		// Domains and certificates
		{G, "/api/domains", Authed, false, s.handleDomainList},
		{P, "/api/domains", Authed, false, s.handleDomainCreate},
		{U, "/api/domains/{id}", Authed, false, s.handleDomainUpdate},
		{D, "/api/domains/{id}", StepUp, false, s.handleDomainDelete},
		{G, "/api/certificates", Authed, false, s.handleCertificates},

		// Routes
		{G, "/api/routes", Authed, false, s.handleRouteList},
		{P, "/api/routes", Authed, false, s.handleRouteCreate},
		{U, "/api/routes/{id}", Authed, false, s.handleRouteUpdate},
		{D, "/api/routes/{id}", Authed, false, s.handleRouteDelete},

		// Containers
		{G, "/api/containers", Authed, false, s.handleContainerList},
		{G, "/api/containers/{id}", Authed, false, s.handleContainerGet},
		{G, "/api/containers/{id}/logs", Authed, false, s.handleContainerLogs},
		{P, "/api/containers/{id}/start", Authed, false, s.handleContainerAction("start")},
		{P, "/api/containers/{id}/stop", Authed, false, s.handleContainerAction("stop")},
		{P, "/api/containers/{id}/restart", Authed, false, s.handleContainerAction("restart")},
		{D, "/api/containers/{id}", StepUp, false, s.handleContainerRemove},

		// Stacks
		{G, "/api/stacks", Authed, false, s.handleStackList},
		{G, "/api/stack-icons", Authed, false, s.handleStackIcons},
		{P, "/api/stacks", Authed, false, s.handleStackCreate},
		{P, "/api/stacks/check", Authed, false, s.handleStackCheck},
		{G, "/api/stacks/{name}", Authed, false, s.handleStackGet},
		{U, "/api/stacks/{name}", Authed, false, s.handleStackUpdate},
		{P, "/api/stacks/{name}/deploy", Authed, false, s.handleStackDeploy},
		{P, "/api/stacks/{name}/down", Authed, false, s.handleStackDown},
		{P, "/api/stacks/{name}/restart", Authed, false, s.handleStackRestart},
		{P, "/api/stacks/{name}/start", Authed, false, s.handleStackStart},
		{P, "/api/stacks/{name}/rename", Authed, false, s.handleStackRename},
		{P, "/api/stacks/{name}/pull", Authed, false, s.handleStackPull},
		{G, "/api/stacks/{name}/services", Authed, false, s.handleStackServices},
		{U, "/api/stacks/{name}/services/{service}", Authed, false, s.handleServiceUpdate},
		{P, "/api/stacks/{name}/services/{service}/start", Authed, false, s.handleServiceAction("start")},
		{P, "/api/stacks/{name}/services/{service}/stop", Authed, false, s.handleServiceAction("stop")},
		{P, "/api/stacks/{name}/services/{service}/restart", Authed, false, s.handleServiceAction("restart")},
		{P, "/api/stacks/{name}/services/{service}/recreate", Authed, false, s.handleServiceAction("recreate")},
		{P, "/api/stacks/{name}/services/{service}/update", Authed, false, s.handleServiceAction("update")},
		{G, "/api/stacks/{name}/icons/{service}", Authed, false, s.handleServiceIcon},
		{U, "/api/stacks/{name}/icons/{service}", Authed, false, s.handleServiceIconSet},
		{D, "/api/stacks/{name}/icons/{service}", Authed, false, s.handleServiceIconDelete},
		{D, "/api/stacks/{name}", StepUp, false, s.handleStackDelete},

		// Volumes and networks
		{G, "/api/volumes", Authed, false, s.handleVolumeList},
		{D, "/api/volumes/{name}", StepUp, false, s.handleVolumeDelete},
		{G, "/api/networks", Authed, false, s.handleNetworkList},

		// Configuration history
		{G, "/api/config/summary", Authed, false, s.handleConfigSummary},
		{G, "/api/config/components/{component}/current", Authed, false, s.handleConfigCurrent},
		{G, "/api/config/components/{component}/history", Authed, false, s.handleConfigHistory},
		{G, "/api/config/versions/{id}", Authed, false, s.handleConfigVersion},
		{P, "/api/config/versions/{id}/rollback", StepUp, false, s.handleConfigRollback},
		{P, "/api/config/apply", Authed, false, s.handleConfigApply},
	}
}
