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

func TestPeopleSurviveAReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	user := store.User("ada")
	user.CopyFolder = "Synaplan/Copies"
	if err := store.PutUser("ada", user); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got := again.User("ada").CopyFolder; got != "Synaplan/Copies" {
		t.Fatalf("copy folder after reload = %q", got)
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
