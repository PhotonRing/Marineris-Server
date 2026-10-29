package api

import (
	"fmt"
	"net/http"

	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/store"
)

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
	// Ticket routes are the page that adds an Authelia passkey, on the
	// portal's host (edge listener only): no session there, a ticket from
	// a step-up route instead, and requests from a portal host.
	Ticket
)

func (a Access) String() string {
	return [...]string{"public", "setup", "authed", "stepup", "ticket"}[a]
}

// Who may call a route, next to its access level. There is no default: the
// zero value is refused at start-up (mustValidRoutes).
type Who int

const (
	unclassified Who = iota
	// Anyone: routes without a session (Public, SetupOnly, Ticket).
	Anyone
	// AnyUser: any signed-in User (their session, Account and the Hub).
	AnyUser
	// Administrator: members of the admins Group.
	Administrator
	// ProxyAdmin: the Owner or a Proxy & SSO Administrator.
	ProxyAdmin
	// Owner: the Owner only.
	Owner
)

func (w Who) String() string {
	return [...]string{"?", "anyone", "user", "admin", "proxy", "owner"}[w]
}

// Route is one API endpoint.
type Route struct {
	Method  string
	Pattern string
	Access  Access
	Who     Who
	// LocalOnly routes are only registered on the local listener.
	LocalOnly bool
	Handler   func(http.ResponseWriter, *http.Request, *reqCtx) error
}

// validateRoutes checks that every route states who may call it, in a way
// that fits its access level: Anyone exactly for routes without a session.
func validateRoutes(routes []Route) error {
	for _, rt := range routes {
		sessionless := rt.Access == Public || rt.Access == SetupOnly || rt.Access == Ticket
		switch {
		case rt.Who <= unclassified || rt.Who > Owner:
			return fmt.Errorf("%s %s: no decision on who may call it", rt.Method, rt.Pattern)
		case sessionless != (rt.Who == Anyone):
			return fmt.Errorf("%s %s: %s access cannot be called by %s", rt.Method, rt.Pattern, rt.Access, rt.Who)
		}
	}
	return nil
}

func mustValidRoutes(routes []Route) {
	if err := validateRoutes(routes); err != nil {
		panic("api route table: " + err.Error())
	}
}

// table is the single list of API endpoints. The security tests iterate it
// and compare it with a golden list, so every new endpoint must be reviewed.
// Each route states who may call it; settings PUT also checks the fields
// that need a Proxy & SSO Administrator (handleSettingsPut).
func (s *Server) table() []Route {
	G, P, U, D := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete
	return []Route{
		// Public
		{G, "/api/health", Public, Anyone, false, s.handleHealth},
		{G, "/api/meta", Public, Anyone, false, s.handleMeta},
		{G, "/api/branding/{asset}", Public, Anyone, false, s.handleBrandingAsset},

		// First-run setup (local listener only, before setup)
		{P, "/api/setup/begin", SetupOnly, Anyone, true, s.handleSetupBegin},
		{P, "/api/setup/finish", SetupOnly, Anyone, true, s.handleSetupFinish},

		// Authentication
		{P, "/api/auth/login", Public, Anyone, false, s.handleLogin},
		{P, "/api/auth/login/mfa", Public, Anyone, false, s.handleLoginMFA},
		{P, "/api/auth/passkey/begin", Public, Anyone, false, s.handlePasskeyLoginBegin},
		{P, "/api/auth/passkey/finish", Public, Anyone, false, s.handlePasskeyLoginFinish},
		{G, "/api/auth/session", Authed, AnyUser, false, s.handleSession},
		{P, "/api/auth/logout", Authed, AnyUser, false, s.handleLogout},
		{P, "/api/auth/reauth", Authed, AnyUser, false, s.handleReauth},

		// Account
		{G, "/api/account/passkeys", Authed, AnyUser, false, s.handlePasskeyList},
		{P, "/api/account/passkeys/begin", StepUp, AnyUser, false, s.handlePasskeyRegisterBegin},
		{P, "/api/account/passkeys/finish", StepUp, AnyUser, false, s.handlePasskeyRegisterFinish},
		{D, "/api/account/passkeys/{id}", StepUp, AnyUser, false, s.handlePasskeyDelete},
		{P, "/api/account/password", StepUp, AnyUser, false, s.handlePasswordChange},
		{P, "/api/account/recovery-codes", StepUp, AnyUser, false, s.handleRecoveryCodes},
		{P, "/api/account/totp/begin", StepUp, AnyUser, false, s.handleTOTPBegin},
		{P, "/api/account/totp/finish", StepUp, AnyUser, false, s.handleTOTPFinish},
		{P, "/api/account/sessions/revoke-others", Authed, AnyUser, false, s.handleRevokeOthers},
		{G, "/api/account/authelia-passkeys", Authed, AnyUser, false, s.handleAutheliaPasskeyList},
		{P, "/api/account/authelia-passkeys", StepUp, AnyUser, false, s.handleAutheliaPasskeyAdd},
		{D, "/api/account/authelia-passkeys/{id}", StepUp, AnyUser, false, s.handleAutheliaPasskeyDelete},

		// The page adding an Authelia passkey, on the portal's host
		{P, caddy.PortalPasskeyPath + "/api/begin", Ticket, Anyone, false, s.handlePortalPasskeyBegin},
		{P, caddy.PortalPasskeyPath + "/api/finish", Ticket, Anyone, false, s.handlePortalPasskeyFinish},

		// Hub (app launcher) and system widgets
		{G, "/api/hub", Authed, AnyUser, false, s.handleHub},
		{G, "/api/hub/icons/{id}", Authed, AnyUser, false, s.handleHubIcon},
		{G, "/api/system/stats", Authed, Administrator, false, s.handleSystemStats},
		{U, "/api/routes/{id}/icon", Authed, Administrator, false, s.handleRouteIconSet},
		{D, "/api/routes/{id}/icon", Authed, Administrator, false, s.handleRouteIconDelete},

		// Overview, settings, audit
		{G, "/api/overview", Authed, Administrator, false, s.handleOverview},
		{G, "/api/settings", Authed, Administrator, false, s.handleSettingsGet},
		{U, "/api/settings", StepUp, Administrator, false, s.handleSettingsPut},
		{G, "/api/audit", Authed, Administrator, false, s.handleAudit},
		{U, "/api/branding", StepUp, Administrator, false, s.handleBrandingPut},
		{U, "/api/branding/{asset}", StepUp, Administrator, false, s.handleBrandingAssetPut},
		{D, "/api/branding/{asset}", StepUp, Administrator, false, s.handleBrandingAssetDelete},

		// Users (who may be acted on: mayActOn, people.go)
		{G, "/api/users", Authed, Administrator, false, s.handleUserList},
		{G, "/api/users/{id}", Authed, Administrator, false, s.handleUserGet},
		{U, "/api/users/{id}", Authed, Administrator, false, s.handleUserUpdate},
		{U, "/api/users/{id}/groups", Authed, Administrator, false, s.handleUserGroups},
		{P, "/api/users/{id}/disable", Authed, Administrator, false, s.handleUserDisable(true)},
		{P, "/api/users/{id}/enable", Authed, Administrator, false, s.handleUserDisable(false)},
		{D, "/api/users/{id}", StepUp, Administrator, false, s.handleUserDelete},

		// Groups (built-ins are listed but changed only by the Owner)
		{G, "/api/groups", Authed, Administrator, false, s.handleGroupList},
		{P, "/api/groups", Authed, Administrator, false, s.handleGroupCreate},
		{U, "/api/groups/{name}", Authed, Administrator, false, s.handleGroupRename},
		{D, "/api/groups/{name}", StepUp, Administrator, false, s.handleGroupDelete},
		{U, "/api/groups/{name}/members/{id}", Authed, Administrator, false, s.handleGroupMember(true)},
		{D, "/api/groups/{name}/members/{id}", Authed, Administrator, false, s.handleGroupMember(false)},

		// Owner powers: the built-in Groups' members (these literal paths
		// win over /api/groups/{name}/...) and ownership handover
		{U, "/api/groups/admins/members/{id}", StepUp, Owner, false, s.handleBuiltinMember(store.GroupAdmins, true)},
		{D, "/api/groups/admins/members/{id}", StepUp, Owner, false, s.handleBuiltinMember(store.GroupAdmins, false)},
		{U, "/api/groups/proxy-admins/members/{id}", StepUp, Owner, false, s.handleBuiltinMember(store.GroupProxyAdmins, true)},
		{D, "/api/groups/proxy-admins/members/{id}", StepUp, Owner, false, s.handleBuiltinMember(store.GroupProxyAdmins, false)},
		{P, "/api/owner", StepUp, Owner, false, s.handleOwnerTransfer},

		// DNS profiles
		{G, "/api/dns/providers", Authed, Administrator, false, s.handleDNSProviders},
		{G, "/api/dns/profiles", Authed, Administrator, false, s.handleDNSList},
		{P, "/api/dns/profiles", Authed, Administrator, false, s.handleDNSCreate},
		{U, "/api/dns/profiles/{id}", StepUp, Administrator, false, s.handleDNSUpdate},
		{D, "/api/dns/profiles/{id}", StepUp, Administrator, false, s.handleDNSDelete},
		{P, "/api/dns/profiles/{id}/test", Authed, Administrator, false, s.handleDNSTest},

		// Domains and certificates
		{G, "/api/domains", Authed, Administrator, false, s.handleDomainList},
		{P, "/api/domains", Authed, Administrator, false, s.handleDomainCreate},
		{U, "/api/domains/{id}", Authed, Administrator, false, s.handleDomainUpdate},
		{D, "/api/domains/{id}", StepUp, Administrator, false, s.handleDomainDelete},
		{G, "/api/certificates", Authed, Administrator, false, s.handleCertificates},

		// Routes
		{G, "/api/routes", Authed, Administrator, false, s.handleRouteList},
		{P, "/api/routes", Authed, Administrator, false, s.handleRouteCreate},
		{U, "/api/routes/{id}", Authed, Administrator, false, s.handleRouteUpdate},
		{D, "/api/routes/{id}", Authed, Administrator, false, s.handleRouteDelete},

		// Containers
		{G, "/api/containers", Authed, Administrator, false, s.handleContainerList},
		{G, "/api/containers/{id}", Authed, Administrator, false, s.handleContainerGet},
		{G, "/api/containers/{id}/logs", Authed, Administrator, false, s.handleContainerLogs},
		{P, "/api/containers/{id}/start", Authed, Administrator, false, s.handleContainerAction("start")},
		{P, "/api/containers/{id}/stop", Authed, Administrator, false, s.handleContainerAction("stop")},
		{P, "/api/containers/{id}/restart", Authed, Administrator, false, s.handleContainerAction("restart")},
		{D, "/api/containers/{id}", StepUp, Administrator, false, s.handleContainerRemove},

		// Stacks
		{G, "/api/stacks", Authed, Administrator, false, s.handleStackList},
		{G, "/api/stack-icons", Authed, Administrator, false, s.handleStackIcons},
		{P, "/api/stacks", Authed, Administrator, false, s.handleStackCreate},
		{P, "/api/stacks/check", Authed, Administrator, false, s.handleStackCheck},
		{G, "/api/stacks/{name}", Authed, Administrator, false, s.handleStackGet},
		{U, "/api/stacks/{name}", Authed, Administrator, false, s.handleStackUpdate},
		{P, "/api/stacks/{name}/deploy", Authed, Administrator, false, s.handleStackDeploy},
		{P, "/api/stacks/{name}/down", Authed, Administrator, false, s.handleStackDown},
		{P, "/api/stacks/{name}/restart", Authed, Administrator, false, s.handleStackRestart},
		{P, "/api/stacks/{name}/start", Authed, Administrator, false, s.handleStackStart},
		{P, "/api/stacks/{name}/rename", Authed, Administrator, false, s.handleStackRename},
		{P, "/api/stacks/{name}/pull", Authed, Administrator, false, s.handleStackPull},
		{G, "/api/stacks/{name}/services", Authed, Administrator, false, s.handleStackServices},
		{U, "/api/stacks/{name}/services/{service}", Authed, Administrator, false, s.handleServiceUpdate},
		{P, "/api/stacks/{name}/services/{service}/start", Authed, Administrator, false, s.handleServiceAction("start")},
		{P, "/api/stacks/{name}/services/{service}/stop", Authed, Administrator, false, s.handleServiceAction("stop")},
		{P, "/api/stacks/{name}/services/{service}/restart", Authed, Administrator, false, s.handleServiceAction("restart")},
		{P, "/api/stacks/{name}/services/{service}/recreate", Authed, Administrator, false, s.handleServiceAction("recreate")},
		{P, "/api/stacks/{name}/services/{service}/update", Authed, Administrator, false, s.handleServiceAction("update")},
		{G, "/api/stacks/{name}/icons/{service}", Authed, Administrator, false, s.handleServiceIcon},
		{U, "/api/stacks/{name}/icons/{service}", Authed, Administrator, false, s.handleServiceIconSet},
		{D, "/api/stacks/{name}/icons/{service}", Authed, Administrator, false, s.handleServiceIconDelete},
		{D, "/api/stacks/{name}", StepUp, Administrator, false, s.handleStackDelete},

		// Volumes and networks
		{G, "/api/volumes", Authed, Administrator, false, s.handleVolumeList},
		{D, "/api/volumes/{name}", StepUp, Administrator, false, s.handleVolumeDelete},
		{G, "/api/networks", Authed, Administrator, false, s.handleNetworkList},

		// Configuration history
		{G, "/api/config/summary", Authed, Administrator, false, s.handleConfigSummary},
		{G, "/api/config/components/{component}/current", Authed, Administrator, false, s.handleConfigCurrent},
		{G, "/api/config/components/{component}/history", Authed, Administrator, false, s.handleConfigHistory},
		{G, "/api/config/versions/{id}", Authed, Administrator, false, s.handleConfigVersion},
		{P, "/api/config/versions/{id}/rollback", StepUp, ProxyAdmin, false, s.handleConfigRollback},
		{P, "/api/config/apply", Authed, Administrator, false, s.handleConfigApply},
	}
}
