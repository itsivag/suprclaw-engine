package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/itsivag/suprclaw/pkg/fileutil"
)

// CommitStore reads and writes commit manifests.
type CommitStore struct {
	dir string // base dir; manifests go under {dir}/{agentID}/{commitID}.json
}

func newCommitStore(dir string) *CommitStore {
	return &CommitStore{dir: dir}
}

func (s *CommitStore) agentDir(agentID string) string {
	return filepath.Join(s.dir, agentID)
}

func (s *CommitStore) manifestPath(agentID, commitID string) string {
	return filepath.Join(s.agentDir(agentID), commitID+".json")
}

// SnapDir returns the snap data directory for a commit.
func (s *CommitStore) SnapDir(agentID, commitID string) string {
	return filepath.Join(s.agentDir(agentID), commitID+".snap.d")
}

// Write derives the commit ID, writes the manifest, and returns the updated manifest.
func (s *CommitStore) Write(m *CommitManifest) (*CommitManifest, error) {
	// Derive ID from canonical JSON (with id="")
	idless := *m
	idless.ID = ""
	canonical, err := json.Marshal(idless)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: marshal manifest: %w", err)
	}
	h := sha256.Sum256(canonical)
	m.ID = hex.EncodeToString(h[:])[:32]

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("checkpoint: marshal manifest: %w", err)
	}

	if err := os.MkdirAll(s.agentDir(m.AgentID), 0o755); err != nil {
		return nil, fmt.Errorf("checkpoint: mkdir agent dir: %w", err)
	}

	path := s.manifestPath(m.AgentID, m.ID)
	if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("checkpoint: write manifest: %w", err)
	}
	return m, nil
}

// Read loads a commit manifest by agentID and commitID.
func (s *CommitStore) Read(agentID, commitID string) (*CommitManifest, error) {
	path := s.manifestPath(agentID, commitID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read manifest: %w", err)
	}
	var m CommitManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("checkpoint: unmarshal manifest: %w", err)
	}
	return &m, nil
}

// List returns all non-revoked commit manifests for an agent, newest-first.
// If sessionKey is non-empty, filters to that session key only.
func (s *CommitStore) List(agentID, sessionKey string) ([]*CommitManifest, error) {
	dir := s.agentDir(agentID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: list commits: %w", err)
	}

	var manifests []*CommitManifest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		commitID := strings.TrimSuffix(e.Name(), ".json")
		m, err := s.Read(agentID, commitID)
		if err != nil {
			continue
		}
		if sessionKey != "" && m.SessionKey != sessionKey {
			continue
		}
		manifests = append(manifests, m)
	}

	// Sort newest-first by CreatedAt
	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].CreatedAt.After(manifests[j].CreatedAt)
	})
	return manifests, nil
}

// Revoke marks a commit as revoked by rewriting its manifest.
func (s *CommitStore) Revoke(agentID, commitID string) (*CommitManifest, error) {
	m, err := s.Read(agentID, commitID)
	if err != nil {
		return nil, err
	}
	if m.Revoked {
		return m, nil // already revoked
	}
	now := timeNow()
	m.Revoked = true
	m.RevokedAt = &now

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("checkpoint: marshal manifest: %w", err)
	}
	path := s.manifestPath(agentID, commitID)
	if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("checkpoint: rewrite manifest: %w", err)
	}
	return m, nil
}

// LatestCommitID returns the ID of the newest commit for an agent+session, or "".
func (s *CommitStore) LatestCommitID(agentID, sessionKey string) string {
	all, err := s.List(agentID, sessionKey)
	if err != nil || len(all) == 0 {
		return ""
	}
	return all[0].ID
}

// PruneOldest removes the oldest commits keeping only maxKeep manifests per agent.
// Snap data directories for removed commits are also deleted.
func (s *CommitStore) PruneOldest(agentID string, maxKeep int) error {
	if maxKeep <= 0 {
		return nil
	}
	all, err := s.List(agentID, "")
	if err != nil {
		return err
	}
	if len(all) <= maxKeep {
		return nil
	}
	toRemove := all[maxKeep:] // oldest entries (List is newest-first)
	for _, m := range toRemove {
		_ = os.Remove(s.manifestPath(agentID, m.ID))
		_ = os.RemoveAll(s.SnapDir(agentID, m.ID))
	}
	return nil
}
