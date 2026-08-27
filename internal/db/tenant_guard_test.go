package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawTenantQueries is a static guard (spec §48): it scans the whole source
// tree for SQL string literals that touch a direct tenant-scoped table without
// organization_id. Queries that legitimately need to omit it (rare) can opt out
// with a trailing `// tenant-scope-exempt: <reason>` comment on the same line.
func TestNoRawTenantQueries(t *testing.T) {
	root := findRepoRoot(t)

	// Match a tenant table referenced after FROM/JOIN/UPDATE/INTO inside code.
	tableRe := regexp.MustCompile(`(?i)\b(?:from|join|update|into)\s+(employees|audit_log)\b`)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "migrations" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, "tenant.go") { // the guard itself
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			l := strings.ToLower(line)
			if !tableRe.MatchString(l) {
				continue
			}
			if strings.Contains(l, "organization_id") || strings.Contains(l, "tenant-scope-exempt") {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d references a tenant table without organization_id (spec §48):\n  %s",
				rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}
