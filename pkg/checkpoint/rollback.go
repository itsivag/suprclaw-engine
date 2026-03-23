package checkpoint

import (
	"fmt"

	"github.com/itsivag/suprclaw/pkg/providers"
)

// RollbackResult describes the outcome of a rollback operation.
type RollbackResult struct {
	RestoredCommit          *CommitManifest      `json:"restored_commit"`
	SessionMessagesRestored int                  `json:"session_messages_restored"`
	WorkspaceFilesRestored  []string             `json:"workspace_files_restored"`
	UnrestoableSideEffects  []ActionEntry        `json:"unrestoable_side_effects"`
	Compensations           []CompensationResult `json:"compensations,omitempty"`
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

// commitCutSeq finds the highest action log seq that references targetCommit.
// all must be newest-first (as returned by actionLog.Query).
// Returns 0 if no entry references the commit.
func commitCutSeq(all []ActionEntry, targetCommitID string) int64 {
	for _, e := range all {
		if e.CommitID == targetCommitID {
			return e.Seq
		}
	}
	return 0
}

// FindSideEffectsBetween returns action entries with side_effect=external that
// occurred after the target commit was created. These represent operations
// that cannot be automatically undone.
func (s *Service) FindSideEffectsBetween(agentID string, targetCommit *CommitManifest) ([]ActionEntry, error) {
	all, err := s.actionLog.Query(agentID, 0)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: query audit: %w", err)
	}

	cutSeq := commitCutSeq(all, targetCommit.ID)

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

// entriesAfterCommit returns all action log entries (oldest-first) with seq > the
// cutSeq derived from the target commit, for use in compensation execution.
func (s *Service) entriesAfterCommit(agentID string, targetCommit *CommitManifest) ([]ActionEntry, error) {
	all, err := s.actionLog.Query(agentID, 0)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: query audit: %w", err)
	}
	cutSeq := commitCutSeq(all, targetCommit.ID)
	return s.actionLog.QueryAfterSeq(agentID, cutSeq)
}
