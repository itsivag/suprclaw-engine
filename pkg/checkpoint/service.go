package checkpoint

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

// timeNow is a variable so tests can override it.
var timeNow = time.Now

// Service is the top-level coordinator for the checkpoint system.
// It owns the action log, commit store, and provides the public API
// used by the admin REST handlers and the agent loop hooks.
type Service struct {
	cfg         *Config
	actionLog   *ActionLog
	commitStore *CommitStore
}

// NewService creates a checkpoint Service rooted at baseDir.
// The layout under baseDir:
//
//	{baseDir}/audit/{agentID}.jsonl
//	{baseDir}/checkpoints/{agentID}/{commitID}.json
//	{baseDir}/checkpoints/{agentID}/{commitID}.snap.d/
func NewService(baseDir string, cfg *Config) *Service {
	if cfg == nil {
		cfg = &Config{}
	}
	return &Service{
		cfg:         cfg,
		actionLog:   newActionLog(filepath.Join(baseDir, "audit")),
		commitStore: newCommitStore(filepath.Join(baseDir, "checkpoints")),
	}
}

// NewHook creates a per-turn ToolHook for the given agent.
// Returns nil when the service is disabled.
func (s *Service) NewHook(agentID, sessionKey, workspace string) *ToolHook {
	if !s.cfg.Enabled {
		return nil
	}
	return newToolHook(s, agentID, sessionKey, workspace, s.cfg)
}

// CreateCommit takes a checkpoint of the current session and workspace state.
// It is called by the hook and by the admin manual-checkpoint endpoint.
func (s *Service) CreateCommit(
	_ context.Context,
	agentID, sessionKey, workspace string,
	msgs []providers.Message,
	label, trigger, parentID string,
) (*CommitManifest, error) {
	maxSize := s.cfg.MaxSnapFileSizeBytes()
	snapshots, err := WalkWorkspace(workspace, maxSize)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: walk workspace: %w", err)
	}

	m := &CommitManifest{
		AgentID:          agentID,
		SessionKey:       sessionKey,
		CreatedAt:        timeNow().UTC(),
		Label:            label,
		Trigger:          trigger,
		ParentID:         parentID,
		SessionLineCount: len(msgs),
		WorkspaceFiles:   snapshots,
		HasSnapData:      s.cfg.StoreSnapData,
	}

	written, err := s.commitStore.Write(m)
	if err != nil {
		return nil, err
	}

	// Store snap data if configured
	if s.cfg.StoreSnapData {
		snapDir := s.commitStore.SnapDir(agentID, written.ID)
		if err := StoreSnapData(snapDir, workspace, snapshots, msgs); err != nil {
			// Log but don't fail the commit; manifest is already written
			logger.WarnCF("checkpoint", "Failed to store snap data",
				map[string]any{"commit_id": written.ID, "error": err.Error()})
			// Rewrite manifest to mark HasSnapData=false
			written.HasSnapData = false
			_, _ = s.commitStore.Write(written)
		}
	}

	// Prune oldest commits
	if s.cfg.MaxCommitsPerAgent > 0 {
		if err := s.commitStore.PruneOldest(agentID, s.cfg.MaxCommitsPerAgent); err != nil {
			logger.WarnCF("checkpoint", "Failed to prune old commits",
				map[string]any{"agent_id": agentID, "error": err.Error()})
		}
	}

	logger.InfoCF("checkpoint", "Commit created",
		map[string]any{
			"agent_id":   agentID,
			"commit_id":  written.ID,
			"label":      label,
			"trigger":    trigger,
			"snap_data":  s.cfg.StoreSnapData,
			"file_count": len(snapshots),
			"msg_count":  len(msgs),
		})
	return written, nil
}

// ListCommits returns commits for an agent, newest-first.
// If sessionKey is non-empty, filters to that session only.
func (s *Service) ListCommits(agentID, sessionKey string) ([]*CommitManifest, error) {
	return s.commitStore.List(agentID, sessionKey)
}

// GetCommit returns a single commit manifest.
func (s *Service) GetCommit(agentID, commitID string) (*CommitManifest, error) {
	return s.commitStore.Read(agentID, commitID)
}

// Rollback restores session and/or workspace state from a commit.
// scope is "session", "workspace", or "all".
// setHistory is called if session rollback is requested.
// workspace is the agent's workspace directory for workspace rollback.
// executor, if non-nil, is used to execute compensation calls for external tool calls
// that occurred after the target commit. Pass nil to skip compensation execution.
func (s *Service) Rollback(
	ctx context.Context,
	agentID, commitID, scope string,
	workspace string,
	setHistory SetHistoryFunc,
	executor ToolExecutorFunc,
) (*RollbackResult, error) {
	commit, err := s.commitStore.Read(agentID, commitID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: load commit: %w", err)
	}

	if !commit.HasSnapData {
		return nil, fmt.Errorf("checkpoint: %w (commit %s has no snap data)", ErrNoSnapData, commitID)
	}

	snapDir := s.commitStore.SnapDir(agentID, commitID)
	result := &RollbackResult{RestoredCommit: commit}

	// Find unrestorable side effects before actually rolling back.
	sideEffects, err := s.FindSideEffectsBetween(agentID, commit)
	if err != nil {
		logger.WarnCF("checkpoint", "Could not query side effects",
			map[string]any{"error": err.Error()})
	}
	result.UnrestoableSideEffects = sideEffects

	// Execute compensations for external tool calls that have a plan.
	if executor != nil {
		allAfter, err := s.entriesAfterCommit(agentID, commit)
		if err != nil {
			logger.WarnCF("checkpoint", "Could not query entries for compensation",
				map[string]any{"error": err.Error()})
		} else {
			compResults := ExecuteCompensations(ctx, allAfter, executor)
			if len(compResults) > 0 {
				result.Compensations = compResults
				// Remove successfully compensated entries from UnrestoableSideEffects.
				compensatedSeqs := make(map[int64]bool, len(compResults))
				for _, cr := range compResults {
					if cr.Error == "" && !cr.Skipped {
						compensatedSeqs[cr.Seq] = true
					}
				}
				filtered := result.UnrestoableSideEffects[:0]
				for _, e := range result.UnrestoableSideEffects {
					if !compensatedSeqs[e.Seq] {
						filtered = append(filtered, e)
					}
				}
				result.UnrestoableSideEffects = filtered
			}
		}
	}

	doSession := scope == "session" || scope == "all"
	doWorkspace := scope == "workspace" || scope == "all"

	if doSession {
		n, err := RollbackSession(snapDir, commit, setHistory)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: session rollback: %w", err)
		}
		result.SessionMessagesRestored = n
	}

	if doWorkspace && workspace != "" {
		restored, err := RollbackWorkspace(snapDir, workspace, commit)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: workspace rollback: %w", err)
		}
		result.WorkspaceFilesRestored = restored
	}

	return result, nil
}

// RevokeCommit marks a commit as revoked.
func (s *Service) RevokeCommit(agentID, commitID string) (*CommitManifest, error) {
	return s.commitStore.Revoke(agentID, commitID)
}

// QueryActionLog returns the most recent limit audit entries for an agent.
func (s *Service) QueryActionLog(agentID string, limit int) ([]ActionEntry, error) {
	return s.actionLog.Query(agentID, limit)
}

// AuditDir returns the audit log directory path.
func (s *Service) AuditDir() string {
	return s.actionLog.dir
}

// CheckpointsDir returns the base checkpoints directory path.
func (s *Service) CheckpointsDir() string {
	return s.commitStore.dir
}

// IsEnabled reports whether the checkpoint service is active.
func (s *Service) IsEnabled() bool {
	return s.cfg != nil && s.cfg.Enabled
}

