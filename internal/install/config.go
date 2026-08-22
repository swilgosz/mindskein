package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// starterConfig is written when there is no config.toml.
//
// Every key is commented out. mindskein has no useful default for a vault
// path, and a file that guesses one would produce a priorities section that
// silently reads the wrong note. Commented keys document what can be set
// without claiming anything is set.
const starterConfig = `# mindskein configuration.
# Uncomment what you need; every key below is optional.

# [vault]
# Absolute path to the vault or notes directory.
# path = "/Users/you/Notes"
# The note holding the !1/!2 priority lines, relative to path or absolute.
# plan = "plan.md"

# [status]
# Sessions quiet for longer than this drop out of the listing. 0 shows all.
# hide_after = "7d"

# [retention]
# How long state is kept before prune deletes it. 0 keeps it forever.
# sessions = "30d"
# handoffs = "90d"
`

// EnsureConfig writes a starter config when there is none, and reports
// whether it created one. An existing file is never touched: it is the user's,
// and an install is not a reason to overwrite it.
func EnsureConfig(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("checking %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
