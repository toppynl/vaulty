package cli

import (
	"encoding/json"
	"os"
	"time"

	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/vault"
)

// openVault resolves the vault root from a.flags.vaultDir and the cwd.
func (a *app) openVault() (*vault.Vault, error) {
	start, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return a.openVaultFrom(start)
}

// openVaultFrom resolves the vault root starting the ancestor search at start
// (used by lint --hook, which starts at the edited file's directory).
func (a *app) openVaultFrom(start string) (*vault.Vault, error) {
	v, err := vault.Open(a.flags.vaultDir, start)
	if err != nil {
		return nil, &ExitError{Code: ExitUsage, Err: err}
	}
	return v, nil
}

// today resolves "today" for --touch, future-date warnings and goldens:
// $VAULTY_TODAY if set, else the local date.
func (a *app) today() string {
	if t := os.Getenv(name.EnvToday); t != "" {
		return t
	}
	return time.Now().Format("2006-01-02")
}

// writeJSON encodes v as one compact-ish JSON document on stdout.
func (a *app) writeJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
