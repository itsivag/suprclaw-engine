package checkpoint

import (
	"fmt"

	"github.com/itsivag/suprclaw/pkg/providers"
)

// RollbackResult describes the outcome of a rollback operation.
type RollbackResult struct {
	RestoredCommit         *CommitManifest `json:"restored_commit"`
	SessionMessagesRestored int            `json:"session_messages_restored"`
	WorkspaceFilesRestored  []string       `json:"workspace_files_restored"`
	UnrestoableSideEffects  []ActionEntry  `json:"unrestoable_side_effects"`
}

// SetHistoryFunc is a callback used to restore session history.
// It matches session.SessionStore.SetHistory's signature without importing that package.
type SetHistoryFunc func(sessionKey string, history []providers.Message)

// RollbackSession restores session history from snap data.
// setHistory is called with the restored messages.
// Returns ErrNoSnapData if snap data was not stored for this commit.
func RollbackSession(snapDir string, commit *CommitManifest, setHistory SetHistoryFunc) (int, error) {
	msgs, err := LoadSessionSnap(snapDir)
	if err != nil {
		return 0, err
	}
	setHistory(commit.SessionKey, msgs)
	return len(msgs), nil
}

// RollbackWorkspace restores workspace files from snap data.
// Returns ErrNoSnapData if snap data was not stored for this commit.
func RollbackWorkspace(snapDir, workspace string, commit *CommitManifest) ([]string, error) {
	return RestoreSnapData(snapDir, workspace, commit)
}

// FindSideEffectsBetween returns action entries with side_effect=external that
// occurred after the target commit was created. These represent operations
// that cannot be automatically undone.
func (s *Service) FindSideEffectsBetween(agentID string, targetCommit *CommitManifest) ([]ActionEntry, error) {
	// Find the sequence number of the last entry at the time of the target commit.
	// We approximate this by looking at entries that reference the targetCommit.ID.
	// A simpler approach: return all external actions that happened after the target.
	// Since action entries are append-only and ordered by seq, we find the seq
	// of the last entry referencing targetCommit and return all external entries after it.

	all, err := s.actionLog.Query(agentID, 0)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: query audit: %w", err)
	}

	// Find the highest seq that references this commitID (or earlier commits).
	// Since all is newest-first, find the first entry (highest seq) that references
	// this commit and note entries after that.
	var cutSeq int64
	for _, e := range all {
		if e.CommitID == targetCommit.ID {
			cutSeq = e.Seq
			break
		}
	}

	var result []ActionEntry
	for _, e := range all {
		if e.Seq <= cutSeq {
			break // all is newest-first
		}
		if e.SideEffect == SideEffectExternal {
			result = append(result, e)
		}
	}
	return result, nil
}
