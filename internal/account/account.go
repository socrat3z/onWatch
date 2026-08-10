// Package account contains safe, provider-neutral account discovery types.
package account

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Definition identifies credential state without ever carrying credential data.
type Definition struct {
	Provider   string
	Name       string
	AuthRoot   string
	ExternalID string
	Metadata   map[string]string
}

// Source reports account membership for one provider.
type Source interface {
	Provider() string
	List(context.Context) ([]Definition, error)
}

// ValidateName permits aliases that are safe as stable labels and path segments.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid account name %q: use 1-32 lowercase letters, numbers, hyphens, or underscores", name)
	}
	return nil
}

// ListDirectories returns valid direct child account directories. It never follows
// symlinks and checks that every resolved child remains under root.
func ListDirectories(root string) ([]string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return nil, fmt.Errorf("account root is required")
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read account root: %w", err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve account root: %w", err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if err := ValidateName(name); err != nil || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !pathWithin(resolvedRoot, resolved) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
