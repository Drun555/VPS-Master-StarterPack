package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStorePersistsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.Users = append(state.Users, User{ID: "user-1", Email: "one@example.org", Links: map[string]UserLink{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Snapshot().Users; len(got) != 1 || got[0].Email != "one@example.org" {
		t.Fatalf("unexpected persisted users: %+v", got)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state is accessible outside owner: %o", info.Mode().Perm())
	}
}

func TestStoreDoesNotCommitRejectedUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	wanted := assertError("reject")
	if err := store.Update(func(state *State) error {
		state.Users = append(state.Users, User{ID: "bad"})
		return wanted
	}); err != wanted {
		t.Fatalf("got %v, want sentinel", err)
	}
	if len(store.Snapshot().Users) != 0 {
		t.Fatal("rejected update mutated in-memory state")
	}
}

type assertError string

func (err assertError) Error() string { return string(err) }
