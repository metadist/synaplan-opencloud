package state

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestUserDefaultsHaveAnArrayOfFolders(t *testing.T) {
	store, err := Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(store.User("nobody"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"readScope":{"mode":"watched","folders":[]},"copyFolder":""}` {
		t.Fatalf("unexpected default user: %s", raw)
	}
}

func TestKeysSurviveAReloadEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user := store.User("ada")
	user.BackgroundKey = "sk_plain"
	user.LinkID = 7
	if err := store.PutUser("ada", user); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstance("pi_1", "instance-secret"); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if again.User("ada").BackgroundKey != "sk_plain" {
		t.Fatal("background key did not survive a reload")
	}
	if id, ok := again.UserByLinkID(7); !ok || id != "ada" {
		t.Fatalf("link id lookup = %q, %v", id, ok)
	}
	if id, secret := again.Instance(); id != "pi_1" || secret != "instance-secret" {
		t.Fatalf("instance = %q / %q", id, secret)
	}
	wrongKey, err := Open(path, "other")
	if err != nil {
		t.Fatal(err)
	}
	if wrongKey.User("ada").BackgroundKey != "" {
		t.Fatal("a wrong state key decrypted the background key")
	}
}

func TestForgetUserDropsTheirSyncedFilesAndWatches(t *testing.T) {
	store, _ := Open("", "")
	_ = store.RememberSynced(Synced{ResourceID: "a", UserID: "ada"})
	_ = store.RememberSynced(Synced{ResourceID: "b", UserID: "bob"})
	_ = store.RememberWatch(Watch{ID: 1, UserID: "ada"})
	_ = store.RememberWatch(Watch{ID: 2, UserID: "bob"})
	if err := store.ForgetUser("ada"); err != nil {
		t.Fatal(err)
	}
	if got := store.Synced(); len(got) != 1 || got[0].UserID != "bob" {
		t.Fatalf("synced after forget = %+v", got)
	}
	if got := store.Watches(); len(got) != 1 || got[0].UserID != "bob" {
		t.Fatalf("watches after forget = %+v", got)
	}
}
