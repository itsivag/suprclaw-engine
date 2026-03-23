package checkpoint

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ActionLog is a goroutine-safe per-agent append-only JSONL audit log.
type ActionLog struct {
	dir  string
	mu   sync.Mutex
	seqs map[string]int64
}

func newActionLog(dir string) *ActionLog {
	return &ActionLog{dir: dir, seqs: make(map[string]int64)}
}

func (l *ActionLog) logPath(agentID string) string {
	return filepath.Join(l.dir, agentID+".jsonl")
}

// Append writes one action entry to the per-agent log.
// It is goroutine-safe; multiple tool goroutines may call it concurrently.
func (l *ActionLog) Append(entry ActionEntry) error {
	l.mu.Lock()
	l.seqs[entry.AgentID]++
	entry.Seq = l.seqs[entry.AgentID]
	if entry.Ts.IsZero() {
		entry.Ts = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	l.mu.Unlock()
	if err != nil {
		return fmt.Errorf("checkpoint: marshal action: %w", err)
	}

	path := l.logPath(entry.AgentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("checkpoint: mkdir audit: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("checkpoint: open audit log: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// Query returns up to limit entries for agentID, newest first.
// If limit <= 0, all entries are returned.
func (l *ActionLog) Query(agentID string, limit int) ([]ActionEntry, error) {
	path := l.logPath(agentID)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: open audit log: %w", err)
	}
	defer f.Close()

	var entries []ActionEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e ActionEntry
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			entries = append(entries, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("checkpoint: scan audit log: %w", err)
	}

	// Reverse so newest is first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// QueryAfterSeq returns entries with seq > afterSeq, for a given agentID.
// Results are returned oldest-first (ascending seq).
func (l *ActionLog) QueryAfterSeq(agentID string, afterSeq int64) ([]ActionEntry, error) {
	all, err := l.Query(agentID, 0)
	if err != nil {
		return nil, err
	}
	// all is newest-first; reverse back to oldest-first
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	var result []ActionEntry
	for _, e := range all {
		if e.Seq > afterSeq {
			result = append(result, e)
		}
	}
	return result, nil
}

// DigestArgs computes a short SHA256 hex digest of the args map.
func DigestArgs(args map[string]any) string {
	data, _ := json.Marshal(args)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:16]
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
