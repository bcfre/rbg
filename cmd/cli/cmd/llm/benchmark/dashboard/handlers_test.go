/*
Copyright 2026 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsPathSafe_SymlinkTraversal tests that symlinks pointing outside the data directory
// are properly rejected, preventing symlink-based path traversal attacks.
func TestIsPathSafe_SymlinkTraversal(t *testing.T) {
	// Create a temporary directory structure:
	// /tmp/xxx/data/          (data directory)
	// /tmp/xxx/outside/       (directory outside data)
	// /tmp/xxx/outside/secret.txt  (sensitive file)
	// /tmp/xxx/data/link -> /tmp/xxx/outside/secret.txt (symlink to outside)

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	outsideDir := filepath.Join(tempDir, "outside")
	secretFile := filepath.Join(outsideDir, "secret.txt")
	symlinkPath := filepath.Join(dataDir, "link")

	// Create directories
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}

	// Create a secret file outside the data directory
	if err := os.WriteFile(secretFile, []byte("sensitive data"), 0644); err != nil {
		t.Fatalf("failed to create secret file: %v", err)
	}

	// Create a symlink inside dataDir pointing to the file outside
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	server := NewServer(dataDir)

	// Test 1: The symlink itself should be detected as unsafe
	// because it resolves to a path outside the data directory
	if server.isPathSafe(symlinkPath) {
		t.Errorf("isPathSafe(%q) = true, want false - symlink pointing outside data dir should be rejected", symlinkPath)
	}

	// Test 2: A normal file inside the data directory should be safe
	normalFile := filepath.Join(dataDir, "normal.txt")
	if err := os.WriteFile(normalFile, []byte("normal data"), 0644); err != nil {
		t.Fatalf("failed to create normal file: %v", err)
	}
	if !server.isPathSafe(normalFile) {
		t.Errorf("isPathSafe(%q) = false, want true - normal file inside data dir should be safe", normalFile)
	}

	// Test 3: A symlink pointing to another file inside dataDir should be safe
	insideTarget := filepath.Join(dataDir, "target.txt")
	if err := os.WriteFile(insideTarget, []byte("target data"), 0644); err != nil {
		t.Fatalf("failed to create inside target file: %v", err)
	}
	insideSymlink := filepath.Join(dataDir, "inside_link")
	if err := os.Symlink(insideTarget, insideSymlink); err != nil {
		t.Fatalf("failed to create inside symlink: %v", err)
	}
	if !server.isPathSafe(insideSymlink) {
		t.Errorf("isPathSafe(%q) = false, want true - symlink pointing inside data dir should be safe", insideSymlink)
	}

	// Test 4: Path traversal using .. should still be rejected
	traversalPath := filepath.Join(dataDir, "..", "outside", "secret.txt")
	if server.isPathSafe(traversalPath) {
		t.Errorf("isPathSafe(%q) = true, want false - path traversal should be rejected", traversalPath)
	}

	// Test 5: Absolute path outside data dir should be rejected
	if server.isPathSafe(secretFile) {
		t.Errorf("isPathSafe(%q) = true, want false - absolute path outside data dir should be rejected", secretFile)
	}
}

// TestIsPathSafe_NonExistentPath tests that non-existent paths are handled correctly
// by checking their parent directory's safety.
func TestIsPathSafe_NonExistentPath(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	outsideDir := filepath.Join(tempDir, "outside")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}

	server := NewServer(dataDir)

	// Test: Non-existent file inside data dir should be safe (parent is safe)
	nonExistentInside := filepath.Join(dataDir, "newdir", "newfile.txt")
	if !server.isPathSafe(nonExistentInside) {
		t.Errorf("isPathSafe(%q) = false, want true - non-existent path inside data dir should be safe", nonExistentInside)
	}

	// Test: Non-existent file outside data dir should be rejected
	nonExistentOutside := filepath.Join(outsideDir, "newfile.txt")
	if server.isPathSafe(nonExistentOutside) {
		t.Errorf("isPathSafe(%q) = true, want false - non-existent path outside data dir should be rejected", nonExistentOutside)
	}
}

// TestIsPathSafe_SymlinkToDirectory tests symlinks that point to directories.
func TestIsPathSafe_SymlinkToDirectory(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	outsideDir := filepath.Join(tempDir, "outside")
	insideSubdir := filepath.Join(dataDir, "subdir")

	if err := os.MkdirAll(insideSubdir, 0755); err != nil {
		t.Fatalf("failed to create inside subdir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}

	server := NewServer(dataDir)

	// Create a symlink inside data pointing to outside directory
	symlinkToOutside := filepath.Join(dataDir, "link_to_outside")
	if err := os.Symlink(outsideDir, symlinkToOutside); err != nil {
		t.Fatalf("failed to create symlink to outside dir: %v", err)
	}

	// This should be rejected
	if server.isPathSafe(symlinkToOutside) {
		t.Errorf("isPathSafe(%q) = true, want false - symlink to outside directory should be rejected", symlinkToOutside)
	}

	// Create a symlink inside data pointing to another directory inside data
	symlinkToInside := filepath.Join(dataDir, "link_to_inside")
	if err := os.Symlink(insideSubdir, symlinkToInside); err != nil {
		t.Fatalf("failed to create symlink to inside dir: %v", err)
	}

	// This should be allowed
	if !server.isPathSafe(symlinkToInside) {
		t.Errorf("isPathSafe(%q) = false, want true - symlink to inside directory should be safe", symlinkToInside)
	}
}

// TestIsPathSafe_MultiLevelSymlink tests nested/chained symlinks.
// Creates: data/link1 -> data/link2 -> outside/secret.txt
func TestIsPathSafe_MultiLevelSymlink(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	outsideDir := filepath.Join(tempDir, "outside")
	secretFile := filepath.Join(outsideDir, "secret.txt")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.WriteFile(secretFile, []byte("sensitive"), 0644); err != nil {
		t.Fatalf("failed to create secret file: %v", err)
	}

	server := NewServer(dataDir)

	// Create a chain of symlinks: link2 -> outside, link1 -> link2
	link2 := filepath.Join(dataDir, "link2")
	link1 := filepath.Join(dataDir, "link1")

	if err := os.Symlink(secretFile, link2); err != nil {
		t.Fatalf("failed to create link2: %v", err)
	}
	if err := os.Symlink(link2, link1); err != nil {
		t.Fatalf("failed to create link1: %v", err)
	}

	// Both symlinks should be rejected as they ultimately resolve to outside
	if server.isPathSafe(link2) {
		t.Errorf("isPathSafe(%q) = true, want false - symlink chain to outside should be rejected", link2)
	}
	if server.isPathSafe(link1) {
		t.Errorf("isPathSafe(%q) = true, want false - nested symlink to outside should be rejected", link1)
	}

	// Test safe chain: data/link4 -> data/link3 -> data/realfile.txt
	realFile := filepath.Join(dataDir, "realfile.txt")
	if err := os.WriteFile(realFile, []byte("safe content"), 0644); err != nil {
		t.Fatalf("failed to create real file: %v", err)
	}

	link3 := filepath.Join(dataDir, "link3")
	link4 := filepath.Join(dataDir, "link4")

	if err := os.Symlink(realFile, link3); err != nil {
		t.Fatalf("failed to create link3: %v", err)
	}
	if err := os.Symlink(link3, link4); err != nil {
		t.Fatalf("failed to create link4: %v", err)
	}

	// Safe chain should be allowed
	if !server.isPathSafe(link3) {
		t.Errorf("isPathSafe(%q) = false, want true - symlink to inside file should be safe", link3)
	}
	if !server.isPathSafe(link4) {
		t.Errorf("isPathSafe(%q) = false, want true - nested symlink staying inside should be safe", link4)
	}
}
