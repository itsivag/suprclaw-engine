package heartbeat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/itsivag/suprclaw/pkg/fileutil"
	"github.com/itsivag/suprclaw/pkg/logger"
)

const (
	historyFileName     = "heartbeat-history.jsonl"
	DefaultHistoryLimit = 100
	MaxHistoryRecords   = 5000
)

type HistoryRecord struct {
	ID                   string          `json:"id"`
	Ts                   int64           `json:"ts"`
	Status               HeartbeatStatus `json:"status"`
	AgentID              string          `json:"agent_id"`
	DurationMs           int64           `json:"duration_ms"`
	Preview              string          `json:"preview,omitempty"`
	SkipReason           string          `json:"skip_reason,omitempty"`
	ConsecutiveOk        int             `json:"consecutive_ok"`
	EffectiveIntervalMin int             `json:"effective_interval_min"`
}

type HistoryFilter struct {
	AgentID  string
	Status   HeartbeatStatus
	BeforeTs int64
	AfterTs  int64
	Limit    int
}

type HistoryStore struct {
	path       string
	maxRecords int

	mu          sync.RWMutex
	records     []HistoryRecord
	recordsByID map[string]int
	unsubscribe func()
	running     bool
}

func NewHistoryStore(workspace string) *HistoryStore {
	return &HistoryStore{
		path:       filepath.Join(workspace, historyFileName),
		maxRecords: MaxHistoryRecords,
	}
}

func (s *HistoryStore) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	records, err := s.loadFromDisk()
	if err != nil {
		return err
	}
	s.records = records
	s.rebuildIndexLocked()
	s.running = true

	s.unsubscribe = OnHeartbeatEvent(func(evt HeartbeatEvent) {
		s.appendEvent(evt)
	})
	return nil
}

func (s *HistoryStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	if s.unsubscribe != nil {
		s.unsubscribe()
		s.unsubscribe = nil
	}
	s.running = false
}

func (s *HistoryStore) List(filter HistoryFilter) []HistoryRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryRecords {
		limit = MaxHistoryRecords
	}

	result := make([]HistoryRecord, 0, limit)
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if filter.AgentID != "" && record.AgentID != filter.AgentID {
			continue
		}
		if filter.Status != "" && record.Status != filter.Status {
			continue
		}
		if filter.BeforeTs > 0 && record.Ts >= filter.BeforeTs {
			continue
		}
		if filter.AfterTs > 0 && record.Ts <= filter.AfterTs {
			continue
		}
		result = append(result, record)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func (s *HistoryStore) Get(id string) (HistoryRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.recordsByID[id]
	if !ok || idx < 0 || idx >= len(s.records) {
		return HistoryRecord{}, false
	}
	return s.records[idx], true
}

func (s *HistoryStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.recordsByID[id]
	if !ok || idx < 0 || idx >= len(s.records) {
		return false, nil
	}

	newRecords := make([]HistoryRecord, 0, len(s.records)-1)
	newRecords = append(newRecords, s.records[:idx]...)
	newRecords = append(newRecords, s.records[idx+1:]...)
	if err := s.rewriteLocked(newRecords); err != nil {
		return false, err
	}
	s.records = newRecords
	s.rebuildIndexLocked()
	return true, nil
}

func (s *HistoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rewriteLocked([]HistoryRecord{}); err != nil {
		return err
	}
	s.records = []HistoryRecord{}
	s.rebuildIndexLocked()
	return nil
}

func (s *HistoryStore) appendEvent(evt HeartbeatEvent) {
	record := HistoryRecord{
		ID:                   "hb_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Ts:                   evt.Ts,
		Status:               evt.Status,
		AgentID:              evt.AgentID,
		DurationMs:           evt.DurationMs,
		Preview:              evt.Preview,
		SkipReason:           evt.SkipReason,
		ConsecutiveOk:        evt.ConsecutiveOk,
		EffectiveIntervalMin: evt.EffectiveIntervalMin,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	if len(s.records) < s.maxRecords {
		if err := s.appendLineLocked(record); err != nil {
			logger.ErrorCF("heartbeat", "failed to append heartbeat history record", map[string]any{"error": err.Error()})
			return
		}
		s.records = append(s.records, record)
		s.recordsByID[record.ID] = len(s.records) - 1
		return
	}

	newRecords := append(append([]HistoryRecord{}, s.records[1:]...), record)
	if err := s.rewriteLocked(newRecords); err != nil {
		logger.ErrorCF("heartbeat", "failed to compact heartbeat history file", map[string]any{"error": err.Error()})
		return
	}
	s.records = newRecords
	s.rebuildIndexLocked()
}

func (s *HistoryStore) loadFromDisk() ([]HistoryRecord, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryRecord{}, nil
		}
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	records := make([]HistoryRecord, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record HistoryRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("invalid heartbeat history jsonl at line %d: %w", lineNo, err)
		}
		if err := validateHistoryRecord(record); err != nil {
			return nil, fmt.Errorf("invalid heartbeat history record at line %d: %w", lineNo, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(records) > s.maxRecords {
		records = append([]HistoryRecord{}, records[len(records)-s.maxRecords:]...)
		if err := s.rewriteRecords(records); err != nil {
			return nil, err
		}
	}

	return records, nil
}

func (s *HistoryStore) appendLineLocked(record HistoryRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *HistoryStore) rewriteLocked(records []HistoryRecord) error {
	return s.rewriteRecords(records)
}

func (s *HistoryStore) rewriteRecords(records []HistoryRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return fileutil.WriteFileAtomic(s.path, buf.Bytes(), 0o600)
}

func (s *HistoryStore) rebuildIndexLocked() {
	index := make(map[string]int, len(s.records))
	for i, record := range s.records {
		index[record.ID] = i
	}
	s.recordsByID = index
}

func validateHistoryRecord(record HistoryRecord) error {
	if record.ID == "" {
		return fmt.Errorf("id is required")
	}
	if record.Ts <= 0 {
		return fmt.Errorf("ts must be > 0")
	}
	if !isValidHeartbeatStatus(record.Status) {
		return fmt.Errorf("status %q is invalid", record.Status)
	}
	if strings.TrimSpace(record.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	return nil
}

func isValidHeartbeatStatus(status HeartbeatStatus) bool {
	switch status {
	case StatusSent, StatusOkToken, StatusOkEmpty, StatusSkipped, StatusFailed:
		return true
	default:
		return false
	}
}

func ParseHeartbeatHistoryLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultHistoryLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("limit must be an integer")
	}
	if value <= 0 || value > MaxHistoryRecords {
		return 0, fmt.Errorf("limit must be between 1 and %d", MaxHistoryRecords)
	}
	return value, nil
}

func ParseHeartbeatHistoryTimestamp(raw, field string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer timestamp in milliseconds", field)
	}
	return value, nil
}

func ParseHeartbeatStatus(raw string) (HeartbeatStatus, error) {
	if raw == "" {
		return "", nil
	}
	status := HeartbeatStatus(raw)
	if !isValidHeartbeatStatus(status) {
		return "", fmt.Errorf("status %q is invalid", raw)
	}
	return status, nil
}

func NewHeartbeatHistoryRecord(
	agentID string,
	status HeartbeatStatus,
	ts time.Time,
) HistoryRecord {
	return HistoryRecord{
		ID:      "hb_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Ts:      ts.UnixMilli(),
		Status:  status,
		AgentID: agentID,
	}
}
