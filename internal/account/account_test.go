package account

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListDirectoriesReturnsOnlySafeAliases(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"work", "personal_2", ".hidden", "UPPER"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListDirectories(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"personal_2", "work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDirectories() = %v, want %v", got, want)
	}
}

func TestAnthropicSourceKeepsMalformedCredentialAccountVisible(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work", ".claude")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".credentials.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := (AnthropicSource{Root: root}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "work" || got[0].Metadata["credentials"] != "present" {
		t.Fatalf("unexpected discovery: %#v", got)
	}
}
