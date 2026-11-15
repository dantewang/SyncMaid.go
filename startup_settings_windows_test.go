package main

import (
	"golang.org/x/sys/windows/registry"
	"syncmaid/internal/taskconfig"
	"testing"
)

func TestStartupIsolatedFixtureAndForeignProtection(t *testing.T) {
	e := startupEntry{name: "SyncMaid.Go.test-" + string(taskconfig.NewID()), command: `"C:\isolated fixture\SyncMaid.exe"`}
	defer func() {
		k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.SET_VALUE)
		if err == nil {
			defer k.Close()
			k.DeleteValue(e.name)
		}
	}()
	if got, err := e.status(); err != nil || got != startupAbsent {
		t.Fatal(got, err)
	}
	if err := e.set(true); err != nil {
		t.Fatal(err)
	}
	if got, err := e.status(); err != nil || got != startupRegistered {
		t.Fatal(got, err)
	}
	approved, _, err := registry.CreateKey(registry.CURRENT_USER, startupApprovedKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer approved.Close()
	defer approved.DeleteValue(e.name)
	b := make([]byte, 12)
	b[0] = 3
	if err := approved.SetBinaryValue(e.name, b); err != nil {
		t.Fatal(err)
	}
	if got, err := e.status(); err != nil || got != startupDisabled {
		t.Fatal(got, err)
	}
	if err := e.set(true); err != nil {
		t.Fatal(err)
	}
	if got, err := e.status(); err != nil || got != startupDisabled {
		t.Fatal("overrode system disabling", got, err)
	}
	other := e
	other.command = `"C:\other\app.exe"`
	if other.set(true) == nil || other.set(false) == nil {
		t.Fatal("foreign command changed")
	}
	if got, err := other.status(); err != nil || got != startupForeign {
		t.Fatal(got, err)
	}
	if err := e.set(false); err != nil {
		t.Fatal(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, startupRunKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetBinaryValue(e.name, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.status(); err == nil {
		t.Fatal("read failure hidden")
	}
	if e.set(true) == nil {
		t.Fatal("invalid foreign value replaced")
	}
	k.DeleteValue(e.name)
	k.Close()
	if got, err := e.status(); err != nil || got != startupAbsent {
		t.Fatal(got, err)
	}
	i, err := currentInstallation()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := currentStartupEntry(i)
	if err != nil {
		t.Fatal(err)
	}
	if actual.command[0] != '"' || actual.command[len(actual.command)-1] != '"' || actual.name == "com.example.syncmaid" || actual.name == "SyncMaid" {
		t.Fatal(actual)
	}

}
