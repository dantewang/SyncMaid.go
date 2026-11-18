package winutil

import (
	"golang.org/x/sys/windows/registry"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestStartupIsolatedFixtureAndForeignProtection(t *testing.T) {
	e := StartupEntry{Name: "SyncMaid.Go.test-" + string(taskconfig.NewID()), Command: `"C:\isolated fixture\SyncMaid.exe"`}
	defer func() {
		k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.SET_VALUE)
		if err == nil {
			defer k.Close()
			k.DeleteValue(e.Name)
		}
	}()
	if got, err := e.Status(); err != nil || got != StartupAbsent {
		t.Fatal(got, err)
	}
	if err := e.Set(true); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Status(); err != nil || got != StartupRegistered {
		t.Fatal(got, err)
	}
	approved, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer approved.Close()
	defer approved.DeleteValue(e.Name)
	b := make([]byte, 12)
	b[0] = 3
	if err := approved.SetBinaryValue(e.Name, b); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Status(); err != nil || got != StartupDisabled {
		t.Fatal(got, err)
	}
	if err := e.Set(true); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Status(); err != nil || got != StartupDisabled {
		t.Fatal("overrode system disabling", got, err)
	}
	other := e
	other.Command = `"C:\other\app.exe"`
	if other.Set(true) == nil || other.Set(false) == nil {
		t.Fatal("foreign command changed")
	}
	if got, err := other.Status(); err != nil || got != StartupForeign {
		t.Fatal(got, err)
	}
	if err := e.Set(false); err != nil {
		t.Fatal(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetBinaryValue(e.Name, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Status(); err == nil {
		t.Fatal("read failure hidden")
	}
	if e.Set(true) == nil {
		t.Fatal("invalid foreign value replaced")
	}
	k.DeleteValue(e.Name)
	k.Close()
	if got, err := e.Status(); err != nil || got != StartupAbsent {
		t.Fatal(got, err)
	}
	i, err := CurrentInstallation()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := CurrentStartupEntry(i)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Command[0] != '"' || actual.Command[len(actual.Command)-1] != '"' || actual.Name == "com.example.syncmaid" || actual.Name == "SyncMaid" {
		t.Fatal(actual)
	}

}
