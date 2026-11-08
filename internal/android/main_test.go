package android

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// makePinnedJar stages a local artifact whose digest is the expected one for
// this test. The real pinned artifact is preferred, so the production hash
// comparison runs whenever the repository contains it.
func makePinnedJar(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "u2.jar")
	for _, candidate := range []string{
		filepath.Join("..", "..", "third_party", "uiautomator", "u2.jar"),
		filepath.Join("..", "..", "service", "src", "mobile_use", "android", "assets", "u2.jar"),
	} {
		if hashOfFile(candidate) != U2JarSHA256 {
			continue
		}
		if raw, err := os.ReadFile(candidate); err == nil {
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatalf("stage pinned jar: %v", err)
			}
			return path
		}
	}
	// Without the repository artifact, a local fixture is registered as the
	// expected digest for the duration of the test only.
	const fake = "unpinned test artifact"
	if err := os.WriteFile(path, []byte(fake), 0o600); err != nil {
		t.Fatalf("stage fixture jar: %v", err)
	}
	previous := artifactDigestUnderTest
	artifactDigestUnderTest = sha256Hex([]byte(fake))
	t.Cleanup(func() { artifactDigestUnderTest = previous })
	return path
}

// hashOfFile returns the lowercase hex SHA-256 of one file, or "" on error.
func hashOfFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

// newCaptureLog returns a logger whose sink is one temporary file.
func newCaptureLog(t *testing.T) (*eventlog.Logger, *os.File) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "events.log"),
		os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return eventlog.New(file, "test"), file
}
