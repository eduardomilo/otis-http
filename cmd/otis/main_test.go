package cli

import (
	"fmt"
	"os"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

// TestMain isolates the CLI's tests from the machine they run on.
//
// Two things about `otis run` reach outside the temporary collection a test
// writes: the OS keyring, and the key index beside settings.json
// (docs/FORMAT.md §5). Left alone, both are the developer's real ones — the
// tests read from the keychain they use every day, and a failed lookup can
// rewrite the index that sits next to their settings.
//
// It is also what made these tests answer differently on different machines.
// A missing secret on macOS is `secret not found`, because there is a keychain
// and it says no; on a Linux CI runner with no Secret Service on the bus it is
// a D-Bus error about org.freedesktop.secrets, because there was nothing to
// ask. Both are correct, and a test asserting the wording of one of them is a
// test asserting which machine it is on. The mock provider is a keyring that
// is present and empty, which is the case the assertions are about.
func TestMain(m *testing.M) {
	keyring.MockInit()

	// os.UserConfigDir reads a different variable on each platform, and
	// DefaultIndexPath is built on it. Set all three: the wrong one being
	// ignored is free, the right one being missed is a write to the user's
	// config directory.
	dir, err := os.MkdirTemp("", "otis-cli-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "otis: isolating the config directory:", err)
		os.Exit(1)
	}
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		if err := os.Setenv(name, dir); err != nil {
			fmt.Fprintln(os.Stderr, "otis: isolating the config directory:", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
