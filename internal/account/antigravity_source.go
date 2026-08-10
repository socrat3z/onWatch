package account

import (
	"context"
	"path/filepath"
)

// AntigravitySource discovers explicitly selected homes. A directory is only a
// candidate; authentication is verified by its account runner, never by reading
// keyring secrets during discovery.
type AntigravitySource struct{ Root string }

func (s AntigravitySource) Provider() string { return "antigravity" }

func (s AntigravitySource) List(ctx context.Context) ([]Definition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := ListDirectories(s.Root)
	if err != nil {
		return nil, err
	}
	definitions := make([]Definition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, Definition{Provider: s.Provider(), Name: name, AuthRoot: filepath.Join(s.Root, name), Metadata: map[string]string{"credentials": "unverified"}})
	}
	return definitions, nil
}
