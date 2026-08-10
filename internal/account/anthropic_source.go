package account

import (
	"context"
	"os"
	"path/filepath"
)

// AnthropicSource discovers Claude Code homes at <root>/<alias>/.claude.
// A malformed credentials file remains visible - the manager can report it as
// unhealthy without accidentally deleting the user's historical telemetry.
type AnthropicSource struct{ Root string }

func (s AnthropicSource) Provider() string { return "anthropic" }

func (s AnthropicSource) List(ctx context.Context) ([]Definition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := ListDirectories(s.Root)
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return nil, err
	}
	definitions := make([]Definition, 0, len(names))
	for _, name := range names {
		path := filepath.Join(s.Root, name, ".claude", ".credentials.json")
		state := "missing"
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if info, statErr := os.Lstat(path); statErr == nil && resolveErr == nil && pathWithin(resolvedRoot, resolved) && info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			state = "present"
		}
		definitions = append(definitions, Definition{Provider: s.Provider(), Name: name, AuthRoot: filepath.Join(s.Root, name), Metadata: map[string]string{"credentials": state}})
	}
	return definitions, nil
}
