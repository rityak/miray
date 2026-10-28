package updater

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestCoreBaseName(t *testing.T) {
	fmt.Println("Core base name =", DefaultCoreUpdater.CoreBaseName())
}

func TestMirayBuildRejectsUpstreamUpdate(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.exe")
	previousVersion := C.Version
	t.Cleanup(func() { C.Version = previousVersion })
	const expected = "Miray builds require a Miray binary; the mihomo updater is disabled"
	updater := &CoreUpdater{}
	for _, version := range []string{"miray-test4-de9f93f3", "v1.19.33-miray.1", "v1.19.33-miray.1-5-gde9f93f3-diag"} {
		C.Version = version
		if err := updater.Update(missing, "", true); err == nil || err.Error() != expected {
			t.Fatalf("Miray version %q: got %v, want %q", version, err, expected)
		}
	}
	C.Version = "miray-v1.19.32-dev"
	// Verify the guard before an existing target could reach any download.
	if err := updater.Update(missing, "", true); err == nil || err.Error() != expected {
		t.Fatalf("Miray missing target: got %v, want %q", err, expected)
	}

	target := filepath.Join(directory, "miray.exe")
	contents := []byte("existing Miray executable")
	if err := os.WriteFile(target, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"", ReleaseChannel, AlphaChannel} {
		if err := updater.Update(target, channel, true); err == nil || err.Error() != expected {
			t.Fatalf("Miray channel %q: got %v, want %q", channel, err, expected)
		}
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, contents) {
		t.Fatalf("updater changed the Miray executable: %q, %v", actual, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "miray.exe" {
		t.Fatalf("updater changed the working directory: %v, %v", entries, err)
	}

	C.Version = "v1.19.32"
	if err := updater.Update(missing, "", false); err == nil || !strings.HasPrefix(err.Error(), "check currentExePath") {
		t.Fatalf("unbranded updater behavior changed: %v", err)
	}
}
