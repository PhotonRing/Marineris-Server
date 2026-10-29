# Glossary

## People and access

**User**:
A person who can sign in, to the apps behind routes and to Marineris itself. There is one kind of User: what one may do depends only on the Groups they belong to.
_Avoid_: account, Authelia user, Marineris account

**Administrator**:
A User who is a member of the `admins` Group. Administrators manage the server; other Users only see the Hub and their Account.
_Avoid_: admin role

**Owner**:
The one Administrator who holds the server: the User created at first-run setup, until they hand ownership to another Administrator. No one else can change, disable, delete or reset the Owner, and only the Owner makes Users Administrators or Proxy & SSO Administrators, or stops them being one.
_Avoid_: super user, root, super admin

**Proxy & SSO Administrator**:
An Administrator the Owner has also put in the built-in `proxy-admins` Group, allowing them to change the Proxy & SSO settings and Overrides that are otherwise the Owner's alone. Stopping being an Administrator ends it too.
_Avoid_: backend admin, SSO admin

**Group**:
A named set of Users, created by Administrators. A User may belong to several Groups; a Group never contains other Groups. The built-in `admins` and `proxy-admins` Groups cannot be deleted; `admins` always keeps at least one member.
_Avoid_: role, team

**Invite**:
A one-time link, valid for a limited time, that an Administrator creates for a new User; the invited person chooses their own password and second factor through it. Users cannot sign up or request an Invite themselves.
_Avoid_: registration, sign-up

**Reset link**:
A one-time link, valid for a limited time, that an Administrator creates for an existing User who lost their password, their second factor or both; the User sets new ones through it, and is signed out everywhere. It is how Users get back in when e-mail is not set up.
_Avoid_: password reset e-mail, recovery link

**Username**:
The name a User signs in with. It never changes once the User exists; the **display name** shown everywhere else can.

**Disabled User**:
A User who can no longer sign in, kept with everything they had so an Administrator can enable them again. Deleting a User, unlike disabling them, removes them for good.
_Avoid_: locked, suspended, banned

**Account**:
The part of Marineris where a User manages their own password, second factor and passkeys. Every User has access to it.
_Avoid_: profile, settings

**Hub**:
The home page listing the apps as tiles. Every User has access to it and sees only the apps they may open; Administrators also see the server widgets.
_Avoid_: dashboard (the status dashboard is a different page), home

## Routes

**Route access**:
Who may reach a route: **Public** (anyone), **Signed-in** (any User, after a two-factor sign-in) or **Restricted** (only the Users and Groups on the route's allow-list, after a two-factor sign-in). There are no deny rules: whoever is not allowed is refused.
_Avoid_: auth, protection, policy

**Allow-list**:
The Users and Groups a Restricted route admits. "Administrators only" is the Allow-list holding the `admins` Group.

## Single sign-on

**OIDC client**:
An app that signs its users in through Marineris's single sign-on instead of keeping its own passwords (e.g. Nextcloud, Immich). Any User may sign in to it unless it is Restricted to an Allow-list, as a route can be; the app also receives the User's Groups to decide what each one may do.
_Avoid_: OAuth app, SSO app, integration

**Override**:
A patch the Owner or a Proxy & SSO Administrator writes, in the file's own format, over the configuration Marineris generates for Caddy or Authelia. It is kept whatever changes are made in the UI afterwards, and cannot touch the settings that keep the server secure.
_Avoid_: raw config, custom config, manual edit

## Monitoring

**Alert**:
A notice to the Administrators that something on the server needs attention (a service down, renewals failing, a disk filling up). It shows in the UI and, when set up, goes out by e-mail and by webhook.
_Avoid_: notification (Authelia's messages), event

**Status dashboard**:
The Administrators' page showing the server's state and its history: graphs, open Alerts, certificates and CrowdSec. It can replace the Hub as their home page.
_Avoid_: dashboard (alone), monitoring page
