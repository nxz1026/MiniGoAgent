package tools

import (
	"testing"
)

func TestValidatePath_WithinWorkspace(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", ".")
	SetWorkspaceRoot(".")
	validPaths := []string{"file.txt", "sub/dir/file.json", "a/b/c.log"}
	for _, p := range validPaths {
		if _, err := ValidatePath(p); err != nil {
			t.Errorf("expected %q to be valid, got: %v", p, err)
		}
	}
}

func TestValidatePath_OutsideWorkspace(t *testing.T) {
	SetWorkspaceRoot(t.TempDir())
	invalidPaths := []string{
		"../escape.txt",
		"../../etc/passwd",
		"sub/../../../etc/shadow",
	}
	for _, p := range invalidPaths {
		if _, err := ValidatePath(p); err == nil {
			t.Errorf("expected %q to be rejected", p)
		}
	}
}
