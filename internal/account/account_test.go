package account

import (
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
