package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// withCanonicalDB points HOME at a throwaway sandbox and creates a populated
// canonical database (~/.onwatch/data/onwatch.db) there. Every test that
// expects a path to be kept runs against this, so a kept path is meaningful:
// the old buggy code would have redirected to the canonical file.
func withCanonicalDB(t *testing.T) (home, canonical string) {
	t.Helper()
	home = t.TempDir()
	testhome.SetTestHome(t, home)
	canonical = filepath.Join(home, ".onwatch", "data", "onwatch.db")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("mkdir canonical dir: %v", err)
	}
	if err := os.WriteFile(canonical, []byte("canonical database contents"), 0o600); err != nil {
		t.Fatalf("write canonical db: %v", err)
	}
	return home, canonical
}

// A --db path that does not exist yet is the first run of an isolated
// instance. It must never be redirected to the production database.
func TestFixExplicitDBPath_FlagMissingFileKept(t *testing.T) {
	_, canonical := withCanonicalDB(t)
	explicit := filepath.Join(t.TempDir(), "iso.db") // does not exist
	setTestArgs(t, []string{"onwatch", "--db", explicit, "--port", "9212"})

	cfg := &config.Config{DBPath: explicit, DBPathExplicit: true}
	fixExplicitDBPath(cfg, testLogger())

	if cfg.DBPath != explicit {
		t.Fatalf("--db path must never be redirected: got %q (canonical %q)", cfg.DBPath, canonical)
	}
}

// The --db=PATH form must be recognised just like --db PATH.
func TestFixExplicitDBPath_FlagEqualsFormKept(t *testing.T) {
	_, canonical := withCanonicalDB(t)
	explicit := filepath.Join(t.TempDir(), "iso.db")
	setTestArgs(t, []string{"onwatch", "--db=" + explicit})

	cfg := &config.Config{DBPath: explicit, DBPathExplicit: true}
	fixExplicitDBPath(cfg, testLogger())

	if cfg.DBPath != explicit {
		t.Fatalf("--db= path must never be redirected: got %q (canonical %q)", cfg.DBPath, canonical)
	}
}

// A --db path smaller than the production database is a legitimate isolated
// instance mid-warmup and must be kept.
func TestFixExplicitDBPath_FlagSmallerFileKept(t *testing.T) {
	_, canonical := withCanonicalDB(t)
	explicit := filepath.Join(t.TempDir(), "iso.db")
	if err := os.WriteFile(explicit, []byte("tiny"), 0o600); err != nil {
		t.Fatalf("write explicit db: %v", err)
	}
	setTestArgs(t, []string{"onwatch", "--db", explicit})

	cfg := &config.Config{DBPath: explicit, DBPathExplicit: true}
	fixExplicitDBPath(cfg, testLogger())

	if cfg.DBPath != explicit {
		t.Fatalf("smaller --db path must never be redirected: got %q (canonical %q)", cfg.DBPath, canonical)
	}
}

// An absolute path from ONWATCH_DB_PATH is an operator's deliberate choice and
// must be kept, even when it does not exist yet.
func TestFixExplicitDBPath_EnvAbsolutePathKept(t *testing.T) {
	_, canonical := withCanonicalDB(t)
	explicit := filepath.Join(t.TempDir(), "iso.db") // does not exist
	setTestArgs(t, []string{"onwatch"})              // no --db flag

	cfg := &config.Config{DBPath: explicit, DBPathExplicit: true}
	fixExplicitDBPath(cfg, testLogger())

	if cfg.DBPath != explicit {
		t.Fatalf("absolute env path must never be redirected: got %q (canonical %q)", cfg.DBPath, canonical)
	}
}

// The original purpose of the function: a legacy relative default such as
// ./onwatch.db (or ./syntrack.db) left in a .env file must still migrate to the
// canonical data path when that file holds the real history.
func TestFixExplicitDBPath_LegacyRelativeDefaultMigrated(t *testing.T) {
	for _, legacy := range []string{"./onwatch.db", "./syntrack.db"} {
		t.Run(legacy, func(t *testing.T) {
			_, canonical := withCanonicalDB(t)
			// Run from an empty dir so the relative path resolves to nothing.
			t.Chdir(t.TempDir())
			setTestArgs(t, []string{"onwatch"}) // no --db flag

			cfg := &config.Config{DBPath: legacy, DBPathExplicit: true}
			fixExplicitDBPath(cfg, testLogger())

			if cfg.DBPath != canonical {
				t.Fatalf("legacy relative default %q should migrate to canonical, got %q", legacy, cfg.DBPath)
			}
		})
	}
}

// A legacy relative default is only migrated when the canonical database
// exists; otherwise it is kept so the user does not lose their (new) data.
func TestFixExplicitDBPath_LegacyRelativeKeptWithoutCanonical(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Chdir(t.TempDir())
	setTestArgs(t, []string{"onwatch"})

	cfg := &config.Config{DBPath: "./onwatch.db", DBPathExplicit: true}
	fixExplicitDBPath(cfg, testLogger())

	if cfg.DBPath != "./onwatch.db" {
		t.Fatalf("legacy default should be kept when canonical is absent, got %q", cfg.DBPath)
	}
}
