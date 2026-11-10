package delegation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/metadist/synaplan-opencloud/internal/idp"
)

func withStore(f *fakeIdP, store Store, failed *[]error) *Delegation {
	return New(Config{
		Provider:     idp.New(f.URL, idp.Endpoints{}, f.Client()),
		ClientID:     "synaplan-opencloud",
		ClientSecret: "secret",
		HTTPClient:   f.Client(),
		Store:        store,
		StoreFailed:  func(err error) { *failed = append(*failed, err) },
	})
}

func TestAConnectedPersonStaysConnectedAcrossARestart(t *testing.T) {
	idp := newIdP(t, "ada", false)
	path := filepath.Join(t.TempDir(), "state", "delegation")
	store, err := NewFileStore(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	var failed []error
	if err := connect(t, withStore(idp, store, &failed), "ada"); err != nil {
		t.Fatal(err)
	}

	restarted := withStore(idp, store, &failed)
	if !restarted.Connected("ada") {
		t.Fatal("the restart forgot the person")
	}
	if _, err := restarted.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	if _, refreshed, _ := idp.state(); len(refreshed) != 1 || refreshed[0] != "offline-1" {
		t.Fatalf("refreshed with %v, want the kept offline-1", refreshed)
	}

	again := withStore(idp, store, &failed)
	if _, err := again.Token(context.Background(), "ada", "synaplan-app"); err != nil {
		t.Fatal(err)
	}
	if _, refreshed, _ := idp.state(); refreshed[len(refreshed)-1] != "offline-2" {
		t.Fatalf("refreshed with %v, want the rotated offline-2 kept", refreshed)
	}

	if err := again.Disconnect(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	if withStore(idp, store, &failed).Connected("ada") {
		t.Fatal("a disconnected person came back after a restart")
	}
	if len(failed) != 0 {
		t.Fatalf("store failed: %v", failed)
	}
}

func TestTheStoreIsSealedWithTheClientSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delegation")
	store, _ := NewFileStore(path, "secret")
	if err := store.Save(map[string]string{"ada": "offline-secret-value"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("offline-secret-value")) || bytes.Contains(raw, []byte("ada")) {
		t.Fatal("the file shows what it keeps")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}

	other, _ := NewFileStore(path, "another secret")
	if _, err := other.Load(); err == nil {
		t.Fatal("the file opened with another client secret")
	}
}

func TestAStoreThatFailsKeepsTokensInMemory(t *testing.T) {
	idp := newIdP(t, "ada", false)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewFileStore(filepath.Join(blocked, "delegation"), "secret")
	var failed []error
	d := withStore(idp, store, &failed)
	if err := connect(t, d, "ada"); err != nil {
		t.Fatal(err)
	}
	if !d.Connected("ada") || len(failed) != 2 {
		t.Fatalf("connected = %v, failures = %v", d.Connected("ada"), failed)
	}

	unreadable, _ := NewFileStore(blocked, "secret")
	failed = nil
	if withStore(idp, unreadable, &failed).Connected("ada") || len(failed) != 1 {
		t.Fatalf("an unreadable store should start empty and say so, failures = %v", failed)
	}
}

func TestNewFileStoreNeedsAPathAndASecret(t *testing.T) {
	for _, args := range [][2]string{{"", "secret"}, {"/tmp/x", ""}} {
		if _, err := NewFileStore(args[0], args[1]); err == nil {
			t.Fatalf("NewFileStore(%q, %q) accepted", args[0], args[1])
		}
	}
}
