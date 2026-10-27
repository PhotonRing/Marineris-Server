package authelia

import (
	"path/filepath"

	"github.com/photonring/marineris-server/internal/secrets"
)

// Secret files read by Authelia through *_FILE environment variables.
var secretFiles = map[string]string{
	"AUTHELIA_IDENTITY_VALIDATION_RESET_PASSWORD_JWT_SECRET_FILE": "jwt",
	"AUTHELIA_SESSION_SECRET_FILE":                                "session",
	"AUTHELIA_STORAGE_ENCRYPTION_KEY_FILE":                        "storage",
}

// EnsureSecrets creates Authelia's secret files in dir when missing. Existing
// secrets are never rotated implicitly: changing the storage key would make
// Authelia's database unreadable.
func EnsureSecrets(dir string) error {
	for _, name := range secretFiles {
		p := filepath.Join(dir, name)
		if _, err := secrets.ReadSecretFile(p); err == nil {
			continue
		}
		if err := secrets.WriteSecretFile(p, []byte(secrets.HexToken(48)), true); err != nil {
			return err
		}
	}
	return nil
}

// EnvArgs returns `docker run` -e arguments pointing Authelia at the secret
// files mounted under mountDir.
func EnvArgs(mountDir string) []string {
	var out []string
	for env, name := range secretFiles {
		out = append(out, "-e", env+"="+mountDir+"/"+name)
	}
	return out
}

// Env returns the environment variables (KEY=value) for the secret files
// mounted under mountDir.
func Env(mountDir string) []string {
	var out []string
	for env, name := range secretFiles {
		out = append(out, env+"="+mountDir+"/"+name)
	}
	return out
}
