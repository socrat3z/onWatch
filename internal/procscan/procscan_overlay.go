package procscan

import (
	"bytes"
	"context"
	"runtime"
	"strings"
)

// Fork overlay: Match and MatchContext report which process matched, for
// callers that need the matched executable or command line rather than a
// bool. They share the platform checks and deadline with RunningContext.

// Match reports the matched command line or Windows image name if running, or "" when none is running.
func Match(windowsImage string, match func(cmdline string) bool) string {
	return MatchContext(context.Background(), windowsImage, match)
}

// MatchContext is Match with a caller-supplied context, bounded by ScanTimeout.
func MatchContext(ctx context.Context, windowsImage string, match func(cmdline string) bool) string {
	ctx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	if runtime.GOOS == "windows" {
		// Same guard as RunningContext: the image name is interpolated into a
		// cmd /C string and must be a bare executable name.
		if !validWindowsImage(windowsImage) {
			return ""
		}
		// tasklist always exits 0; findstr verifies a real match.
		query := `tasklist /FI "IMAGENAME eq ` + windowsImage + `" /NH 2>nul | findstr /I "` + windowsImage + `"`
		if err := runCommandContext(ctx, "cmd", "/C", query); err == nil {
			return windowsImage
		}
		return ""
	}
	if match == nil {
		return ""
	}
	out, err := execCommandContext(ctx, "ps", "-Ao", "args=")
	if err != nil {
		return ""
	}
	return ScanMatch(out, match)
}

// ScanMatch reports the first command line in psOutput satisfying match, or "".
// Each line is trimmed before it is matched and returned.
func ScanMatch(psOutput []byte, match func(cmdline string) bool) string {
	if match == nil {
		return ""
	}
	for _, line := range bytes.Split(psOutput, []byte("\n")) {
		candidate := strings.TrimSpace(string(line))
		if match(candidate) {
			return candidate
		}
	}
	return ""
}
