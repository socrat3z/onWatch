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

func TestAnthropicSourceToleratesAbsentRoot(t *testing.T) {
	defs, err := (AnthropicSource{Root: filepath.Join(t.TempDir(), "never-mounted")}).List(context.Background())
	if err != nil {
		t.Fatalf("an unmounted root must not be an error, it would warn every minute forever: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected no accounts from an absent root, got %d", len(defs))
	}
}

func TestAnthropicSourceStillContainmentChecksAPresentRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "work", ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "work", ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	// An escaping symlink must never be reported as a present credential file.
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(outside, []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600); err != nil {
		t.Fatalf("write outside credentials: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "escaped", ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped", ".claude", ".credentials.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	defs, err := (AnthropicSource{Root: root}).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	states := map[string]string{}
	for _, def := range defs {
		states[def.Name] = def.Metadata["credentials"]
	}
	if states["work"] != "present" {
		t.Fatalf("a real credential file must resolve as present, got %q", states["work"])
	}
	if states["escaped"] != "missing" {
		t.Fatalf("a credential symlink pointing outside the root must not count as present, got %q", states["escaped"])
	}
}
