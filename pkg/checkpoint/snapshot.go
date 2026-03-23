package checkpoint

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/itsivag/suprclaw/pkg/fileutil"
	"github.com/itsivag/suprclaw/pkg/providers"
)

// ErrNoSnapData is returned when rollback is attempted but snap data was not stored.
var ErrNoSnapData = errors.New("checkpoint: no snap data stored for this commit")

const (
	sessionSnapFile = "_session.json"
	absentFile      = "ABSENT"
)

// WalkWorkspace collects FileSnapshot metadata for all files under workspace.
// Skips the "sessions/" subdirectory and files larger than maxFileSize.
func WalkWorkspace(workspace string, maxFileSize int64) ([]FileSnapshot, error) {
	var snaps []FileSnapshot
	err := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		rel, _ := filepath.Rel(workspace, path)
		if rel == "." {
			return nil
		}

		// Skip sessions directory
		if d.IsDir() {
			if d.Name() == "sessions" {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip files that are too large
		if maxFileSize > 0 && info.Size() > maxFileSize {
			return nil
		}

		digest, err := fileDigest(path)
		if err != nil {
			return nil
		}

		snaps = append(snaps, FileSnapshot{
			RelPath:     filepath.ToSlash(rel),
			SHA256:      digest,
			Size:        info.Size(),
			ModTimeUnix: info.ModTime().Unix(),
		})
		return nil
	})
	return snaps, err
}

// fileDigest returns the SHA256 hex digest of a file's content.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// StoreSnapData stores workspace files and session messages under snapDir.
// Only files listed in snapshots are copied.
func StoreSnapData(snapDir, workspace string, snapshots []FileSnapshot, msgs []providers.Message) error {
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return fmt.Errorf("checkpoint: mkdir snap: %w", err)
	}

	// Store workspace files
	for _, snap := range snapshots {
		src := filepath.Join(workspace, filepath.FromSlash(snap.RelPath))
		dst := filepath.Join(snapDir, filepath.FromSlash(snap.RelPath))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("checkpoint: copy %s: %w", snap.RelPath, err)
		}
	}

	// Store session messages
	if msgs != nil {
		data, err := json.Marshal(msgs)
		if err != nil {
			return fmt.Errorf("checkpoint: marshal session: %w", err)
		}
		if err := fileutil.WriteFileAtomic(filepath.Join(snapDir, sessionSnapFile), data, 0o644); err != nil {
			return fmt.Errorf("checkpoint: write session snap: %w", err)
		}
	}

	return nil
}

// LoadSessionSnap reads the session messages from a snap directory.
func LoadSessionSnap(snapDir string) ([]providers.Message, error) {
	path := filepath.Join(snapDir, sessionSnapFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ErrNoSnapData
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read session snap: %w", err)
	}
	var msgs []providers.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("checkpoint: unmarshal session snap: %w", err)
	}
	return msgs, nil
}

// RestoreSnapData restores workspace files from snapDir to workspace.
// Files that existed in the snap but not in the current manifest are restored.
// Files that are in the current workspace but NOT in manifest.WorkspaceFiles are deleted.
// Returns the list of restored relative paths.
func RestoreSnapData(snapDir, workspace string, manifest *CommitManifest) ([]string, error) {
	// Check snap dir exists
	if _, err := os.Stat(snapDir); os.IsNotExist(err) {
		return nil, ErrNoSnapData
	}

	// Build a set of files that should exist after rollback
	shouldExist := make(map[string]bool, len(manifest.WorkspaceFiles))
	for _, snap := range manifest.WorkspaceFiles {
		shouldExist[filepath.FromSlash(snap.RelPath)] = true
	}

	// Restore files from snap.d to workspace
	var restored []string
	for _, snap := range manifest.WorkspaceFiles {
		relNative := filepath.FromSlash(snap.RelPath)
		src := filepath.Join(snapDir, relNative)
		dst := filepath.Join(workspace, relNative)

		if _, err := os.Stat(src); os.IsNotExist(err) {
			// Snap doesn't have this file's content; skip (shouldn't normally happen)
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return restored, fmt.Errorf("checkpoint: restore %s: %w", snap.RelPath, err)
		}
		restored = append(restored, snap.RelPath)
	}

	// Delete workspace files that didn't exist at checkpoint time
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == "sessions" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		if !shouldExist[rel] {
			_ = os.Remove(path)
		}
		return nil
	})

	return restored, nil
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	data, err := readAll(in)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(dst, data, info.Mode())
}

func readAll(r io.Reader) ([]byte, error) {
	buf := bufio.NewReader(r)
	var out []byte
	tmp := make([]byte, 32*1024)
	for {
		n, err := buf.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

