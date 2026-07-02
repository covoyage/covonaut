package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const CurrentVersion int64 = 4

// ---------------------------------------------------------------------------
// Entry types — mirrors the session-manager.ts entry taxonomy
// ---------------------------------------------------------------------------

type EntryType string

const (
	EntryHeader        EntryType = "session"
	EntryMessage       EntryType = "message"
	EntrySessionInfo   EntryType = "session_info"
	EntryModelChange   EntryType = "model_change"
	EntryCompaction    EntryType = "compaction"
	EntryBranchSummary EntryType = "branch_summary"
	EntryLabel         EntryType = "label"
	EntryCustom        EntryType = "custom"
	EntryCustomMessage EntryType = "custom_message"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

// Header is always the first entry in a JSONL session file.
type Header struct {
	Type          EntryType `json:"type"`
	Version       int64     `json:"version"`
	ID            string    `json:"id"`
	Timestamp     string    `json:"timestamp"`
	Cwd           string    `json:"cwd,omitempty"`
	ParentSession string    `json:"parent_session,omitempty"`
}

// Entry is one line in the JSONL file. Forms a tree via ID/ParentID.
type Entry struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parent_id,omitempty"`
	Type      EntryType       `json:"type"`
	Data      json.RawMessage `json:"data"`
	Timestamp time.Time       `json:"timestamp"`
	Version   int64           `json:"version,omitempty"`
}

// LabelData is the payload of an EntryLabel.
type LabelData struct {
	TargetID string `json:"target_id"`
	Label    string `json:"label,omitempty"`
}

// CompactionData is the payload of an EntryCompaction.
type CompactionData struct {
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"first_kept_entry_id"`
	KeptCount        int64  `json:"kept_count"`
}

// BranchSummaryData is the payload of an EntryBranchSummary.
type BranchSummaryData struct {
	Summary  string `json:"summary"`
	BranchID string `json:"branch_id"`
}

// Info is the public metadata of a session for listing.
type Info struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	Label         string    `json:"label,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ParentSession string    `json:"parent_session,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	MessageCount  int64     `json:"message_count"`
	Version       int64     `json:"version"`
}

// ---------------------------------------------------------------------------
// Store interface
// ---------------------------------------------------------------------------

type Store interface {
	Create(ctx context.Context, opts CreateOptions) (*Manager, error)
	Open(ctx context.Context, sessionID string) (*Manager, error)
	List(ctx context.Context) ([]Info, error)
	Delete(ctx context.Context, sessionID string) error
}

type CreateOptions struct {
	ID            string
	Cwd           string
	ParentSession string
	InMemory      bool
}

// ---------------------------------------------------------------------------
// Manager — the heart: manages one session's lifecycle (append-only tree)
// ---------------------------------------------------------------------------

type Manager struct {
	header  Header
	entries []Entry
	leafID  string

	filePath string
	persist  bool
	flushed  bool

	labelsById map[string]string
	index      map[string]*Entry

	mu        sync.RWMutex
	idMu      sync.Mutex
	idCounter int64
}

func newManager(header Header, filePath string, persist bool) *Manager {
	return &Manager{
		header:     header,
		filePath:   filePath,
		persist:    persist,
		labelsById: make(map[string]string),
		index:      make(map[string]*Entry),
	}
}

// Header returns the session header.
func (m *Manager) Header() Header {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.header
}

// LeafID returns the current leaf (cursor) in the tree.
func (m *Manager) LeafID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leafID
}

// Entries returns a copy of all entries.
func (m *Manager) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]Entry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

// Append adds a new entry as a child of the current leaf.
func (m *Manager) Append(_ context.Context, entry Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = m.generateID()
	}
	if entry.ParentID == "" {
		entry.ParentID = m.leafID
	}
	if entry.Version == 0 {
		entry.Version = CurrentVersion
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	m.entries = append(m.entries, entry)
	m.index[entry.ID] = &m.entries[len(m.entries)-1]
	m.leafID = entry.ID

	if entry.Type == EntryLabel {
		var ld LabelData
		if err := json.Unmarshal(entry.Data, &ld); err == nil {
			if ld.Label == "" {
				delete(m.labelsById, ld.TargetID)
			} else {
				m.labelsById[ld.TargetID] = ld.Label
			}
		}
	}

	if err := m.persistEntry(entry); err != nil {
		return err
	}
	return nil
}

// AppendMessage is a convenience for adding a message entry.
func (m *Manager) AppendMessage(ctx context.Context, msg agentcore.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return m.Append(ctx, Entry{Type: EntryMessage, Data: data})
}

// AppendCompaction records a compaction event.
func (m *Manager) AppendCompaction(ctx context.Context, cd CompactionData) error {
	data, err := json.Marshal(cd)
	if err != nil {
		return fmt.Errorf("marshal compaction: %w", err)
	}
	return m.Append(ctx, Entry{Type: EntryCompaction, Data: data})
}

// AppendBranchSummary records a branch summary.
func (m *Manager) AppendBranchSummary(ctx context.Context, bs BranchSummaryData) error {
	data, err := json.Marshal(bs)
	if err != nil {
		return fmt.Errorf("marshal branch summary: %w", err)
	}
	return m.Append(ctx, Entry{Type: EntryBranchSummary, Data: data})
}

// SetLabel adds or removes a label on an entry.
func (m *Manager) SetLabel(ctx context.Context, targetID, label string) error {
	data, err := json.Marshal(LabelData{TargetID: targetID, Label: label})
	if err != nil {
		return fmt.Errorf("marshal label: %w", err)
	}
	return m.Append(ctx, Entry{Type: EntryLabel, Data: data})
}

// SetSessionName writes a session_info entry with a display name.
func (m *Manager) SetSessionName(ctx context.Context, name string) error {
	data, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return fmt.Errorf("marshal session name: %w", err)
	}
	return m.Append(ctx, Entry{Type: EntrySessionInfo, Data: data})
}

// Branch moves the leaf pointer to an earlier entry without deleting history.
func (m *Manager) Branch(branchFromID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.index[branchFromID]; !ok {
		return fmt.Errorf("session: entry %q not found", branchFromID)
	}
	m.leafID = branchFromID
	return nil
}

// GetLabel returns the label for an entry, if any.
func (m *Manager) GetLabel(entryID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labelsById[entryID]
}

// MessagesOnPath extracts Messages along the root→leaf path.
func (m *Manager) MessagesOnPath() []agentcore.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path := m.pathToLeaf()
	var msgs []agentcore.Message
	var lastCompaction *CompactionData

	for _, entry := range path {
		if entry.Type == EntryCompaction {
			var cd CompactionData
			if json.Unmarshal(entry.Data, &cd) == nil {
				lastCompaction = &cd
			}
		}
	}

	skipUntil := ""
	if lastCompaction != nil {
		skipUntil = lastCompaction.FirstKeptEntryID
		msgs = append(msgs, agentcore.Message{
			Role:    agentcore.RoleSystem,
			Content: lastCompaction.Summary,
			Type:    agentcore.MessageTypeCompactionSummary,
		})
	}

	skipping := skipUntil != ""
	for _, entry := range path {
		if skipping {
			if entry.ID == skipUntil {
				skipping = false
			} else {
				continue
			}
		}

		switch entry.Type {
		case EntryMessage:
			var msg agentcore.Message
			if json.Unmarshal(entry.Data, &msg) == nil {
				msgs = append(msgs, msg)
			}
		case EntryBranchSummary:
			var bs BranchSummaryData
			if json.Unmarshal(entry.Data, &bs) == nil {
				msgs = append(msgs, agentcore.Message{
					Role:    agentcore.RoleSystem,
					Content: bs.Summary,
					Type:    agentcore.MessageTypeBranchSummary,
				})
			}
		}
	}

	return msgs
}

// Info returns the public metadata for this session.
func (m *Manager) Info() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info := Info{
		ID:            m.header.ID,
		ParentSession: m.header.ParentSession,
		Cwd:           m.header.Cwd,
		Version:       m.header.Version,
	}

	if len(m.entries) > 0 {
		info.CreatedAt = m.entries[0].Timestamp
		info.UpdatedAt = m.entries[len(m.entries)-1].Timestamp
	}

	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.Type == EntrySessionInfo {
			var meta map[string]string
			if json.Unmarshal(e.Data, &meta) == nil {
				if v, ok := meta["name"]; ok && info.Name == "" {
					info.Name = v
				}
			}
		}
	}

	for _, e := range m.entries {
		if e.Type == EntryMessage {
			info.MessageCount++
		}
	}

	return info
}

// CreateBranchedSession extracts root→leafID path into a new JSONL file.
// Returns the new session's file path.
func (m *Manager) CreateBranchedSession(ctx context.Context, store *FileStore) (string, error) {
	m.mu.RLock()
	path := m.pathToLeaf()
	m.mu.RUnlock()

	newID := m.generateID()
	newMgr, err := store.Create(ctx, CreateOptions{
		ID:            newID,
		Cwd:           m.header.Cwd,
		ParentSession: m.header.ID,
	})
	if err != nil {
		return "", fmt.Errorf("create branched session: %w", err)
	}

	for _, entry := range path {
		if entry.Type == EntryLabel {
			continue
		}
		entry.Version = CurrentVersion
		if err := newMgr.Append(ctx, entry); err != nil {
			return "", fmt.Errorf("copy entry to branch: %w", err)
		}
	}

	m.mu.RLock()
	for _, entry := range path {
		if label, ok := m.labelsById[entry.ID]; ok {
			_ = newMgr.SetLabel(ctx, entry.ID, label)
		}
	}
	m.mu.RUnlock()

	return newID, nil
}

// pathToLeaf returns the ordered list of entries from root to current leaf.
func (m *Manager) pathToLeaf() []Entry {
	if m.leafID == "" {
		return nil
	}

	parentMap := make(map[string]string)
	entryMap := make(map[string]Entry)
	for _, e := range m.entries {
		parentMap[e.ID] = e.ParentID
		entryMap[e.ID] = e
	}

	var chain []Entry
	visited := make(map[string]bool)
	current := m.leafID
	for current != "" {
		if visited[current] {
			break
		}
		visited[current] = true
		if e, ok := entryMap[current]; ok {
			chain = append(chain, e)
		}
		current = parentMap[current]
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// ---------------------------------------------------------------------------
// Persistence (lazy flush)
// ---------------------------------------------------------------------------

func (m *Manager) persistEntry(entry Entry) error {
	if !m.persist || m.filePath == "" {
		return nil
	}

	hasAssistant := false
	for _, e := range m.entries {
		if e.Type == EntryMessage {
			var msg agentcore.Message
			if json.Unmarshal(e.Data, &msg) == nil && msg.Role == agentcore.RoleAssistant {
				hasAssistant = true
				break
			}
		}
	}
	if !hasAssistant {
		m.flushed = false
		return nil
	}

	if !m.flushed {
		return m.flushAll()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	f, err := os.OpenFile(m.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock session file: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	return nil
}

func (m *Manager) flushAll() error {
	f, err := os.OpenFile(m.filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create session file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock session file: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	headerData, err := json.Marshal(m.header)
	if err != nil {
		return fmt.Errorf("marshal header: %w", err)
	}
	if _, err := f.Write(append(headerData, '\n')); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, e := range m.entries {
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal entry: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write entry: %w", err)
		}
	}
	m.flushed = true
	return nil
}

// ---------------------------------------------------------------------------
// Version migration
// ---------------------------------------------------------------------------

func migrateEntries(entries []Entry, fromVersion int64) bool {
	changed := false
	if fromVersion < 2 {
		migrateV1ToV2(entries)
		changed = true
	}
	if fromVersion < 3 {
		migrateV2ToV3(entries)
		changed = true
	}
	if fromVersion < 4 {
		migrateV3ToV4(entries)
		changed = true
	}
	return changed
}

func migrateV1ToV2(entries []Entry) {
	var lastID string
	for i := range entries {
		if entries[i].ID == "" {
			entries[i].ID = generateID()
		}
		if entries[i].ParentID == "" && lastID != "" {
			entries[i].ParentID = lastID
		}
		lastID = entries[i].ID
		entries[i].Version = 2
	}
}

func migrateV2ToV3(entries []Entry) {
	for i := range entries {
		if entries[i].Type == EntryCompaction {
			var raw map[string]any
			if json.Unmarshal(entries[i].Data, &raw) == nil {
				if _, ok := raw["firstKeptEntryIndex"]; ok {
					delete(raw, "firstKeptEntryIndex")
					updated, _ := json.Marshal(raw)
					entries[i].Data = updated
				}
			}
		}
		entries[i].Version = 3
	}
}

func migrateV3ToV4(entries []Entry) {
	for i := range entries {
		if entries[i].Type == EntryMessage {
			var msg map[string]any
			if json.Unmarshal(entries[i].Data, &msg) == nil {
				if role, ok := msg["role"].(string); ok && role == "hookMessage" {
					msg["role"] = "custom"
					updated, _ := json.Marshal(msg)
					entries[i].Data = updated
				}
			}
		}
		entries[i].Version = 4
	}
}

// ---------------------------------------------------------------------------
// FileStore — one JSONL file per session
// ---------------------------------------------------------------------------

type FileStore struct {
	dir     string
	locks   map[string]*sync.RWMutex
	locksMu sync.Mutex

	// maxLocks caps the per-session lock cache. When the cache exceeds this
	// limit the entire map is swapped for a fresh one under the global mutex.
	// This prevents unbounded memory growth from long-running processes that
	// create many sessions over time. The default (0) means unlimited.
	maxLocks int
}

// FileStoreOption configures a FileStore.
type FileStoreOption func(*FileStore)

// WithMaxLocks sets the maximum number of per-session locks cached by the
// FileStore. When the cache exceeds this size it is pruned. Set to 0 for
// unlimited (the default).
func WithMaxLocks(n int) FileStoreOption {
	return func(fs *FileStore) { fs.maxLocks = n }
}

func NewFileStore(dir string, opts ...FileStoreOption) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	fs := &FileStore{
		dir:   dir,
		locks: make(map[string]*sync.RWMutex),
	}
	for _, opt := range opts {
		opt(fs)
	}
	return fs, nil
}

func (s *FileStore) sessionLock(id string) *sync.RWMutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if l, ok := s.locks[id]; ok {
		return l
	}

	// Prune the lock cache when it exceeds the configured limit.
	// Old locks that are no longer in active use are safe to discard
	// because any concurrent holder still has a reference to their *sync.RWMutex.
	if s.maxLocks > 0 && len(s.locks) >= s.maxLocks {
		s.locks = make(map[string]*sync.RWMutex, s.maxLocks)
	}

	l := &sync.RWMutex{}
	s.locks[id] = l
	return l
}

func (s *FileStore) path(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".jsonl")
}

func (s *FileStore) Create(_ context.Context, opts CreateOptions) (*Manager, error) {
	if opts.ID == "" {
		opts.ID = generateID()
	}

	header := Header{
		Type:          EntryHeader,
		Version:       CurrentVersion,
		ID:            opts.ID,
		Timestamp:     time.Now().Format(time.RFC3339),
		Cwd:           opts.Cwd,
		ParentSession: opts.ParentSession,
	}

	persist := !opts.InMemory
	filePath := ""
	if persist {
		filePath = s.path(opts.ID)
	}

	mgr := newManager(header, filePath, persist)

	if persist {
		headerData, _ := json.Marshal(header)
		if err := os.WriteFile(filePath, append(headerData, '\n'), 0o644); err != nil {
			return nil, fmt.Errorf("write session header: %w", err)
		}
		mgr.flushed = true
	}

	return mgr, nil
}

func (s *FileStore) Open(_ context.Context, sessionID string) (*Manager, error) {
	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	f, err := os.Open(s.path(sessionID))
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var header Header
	var entries []Entry
	headerParsed := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if !headerParsed {
			var raw map[string]any
			if err := json.Unmarshal([]byte(line), &raw); err == nil {
				if t, ok := raw["type"].(string); ok && t == string(EntryHeader) {
					if err := json.Unmarshal([]byte(line), &header); err != nil {
						return nil, fmt.Errorf("parse session header: %w", err)
					}
					headerParsed = true
					continue
				}
			}
			header = Header{
				Type:    EntryHeader,
				Version: 1,
				ID:      sessionID,
			}
			headerParsed = true
		}

		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}

	if !headerParsed {
		return nil, fmt.Errorf("session %q: empty or invalid file", sessionID)
	}

	fromVersion := header.Version
	if fromVersion == 0 {
		fromVersion = 1
	}

	needsRewrite := false
	if fromVersion < CurrentVersion {
		needsRewrite = migrateEntries(entries, fromVersion)
		header.Version = CurrentVersion
	}

	mgr := newManager(header, s.path(sessionID), true)
	mgr.entries = entries
	mgr.flushed = true

	for i := range mgr.entries {
		e := &mgr.entries[i]
		mgr.index[e.ID] = e
	}

	for _, e := range entries {
		if e.Type == EntryLabel {
			var ld LabelData
			if json.Unmarshal(e.Data, &ld) == nil {
				if ld.Label == "" {
					delete(mgr.labelsById, ld.TargetID)
				} else {
					mgr.labelsById[ld.TargetID] = ld.Label
				}
			}
		}
	}

	if len(entries) > 0 {
		mgr.leafID = entries[len(entries)-1].ID
	}

	if needsRewrite {
		if err := mgr.flushAll(); err != nil {
			return nil, err
		}
	}

	return mgr, nil
}

func (s *FileStore) List(_ context.Context) ([]Info, error) {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list session directory: %w", err)
	}

	var sessions []Info
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".jsonl")
		info := s.readInfo(name)
		sessions = append(sessions, info)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func (s *FileStore) Delete(_ context.Context, sessionID string) error {
	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	if err := os.Remove(s.path(sessionID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete session: %w", err)
	}

	s.locksMu.Lock()
	delete(s.locks, sessionID)
	s.locksMu.Unlock()

	return nil
}

// lockCleanup removes the per-session lock cache entry without touching the
// underlying file. Useful when a session file has already been replaced
// (e.g. during rewriteSession) and only the stale lock needs to be purged.
func (s *FileStore) lockCleanup(sessionID string) {
	s.locksMu.Lock()
	delete(s.locks, sessionID)
	s.locksMu.Unlock()
}

func (s *FileStore) Has(_ context.Context, sessionID string) (bool, error) {
	_, err := os.Stat(s.path(sessionID))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("check session: %w", err)
}

func (s *FileStore) readInfo(sessionID string) Info {
	info := Info{ID: sessionID}

	f, err := os.Open(s.path(sessionID))
	if err != nil {
		return info
	}
	defer f.Close()

	fi, err := f.Stat()
	if err == nil {
		info.UpdatedAt = fi.ModTime()
		info.CreatedAt = fi.ModTime()
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	lineNum := int64(0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineNum++

		if lineNum == 1 {
			var header Header
			if json.Unmarshal([]byte(line), &header) == nil && header.Type == EntryHeader {
				info.ParentSession = header.ParentSession
				info.Cwd = header.Cwd
				info.Version = header.Version
				if t, err := time.Parse(time.RFC3339, header.Timestamp); err == nil {
					info.CreatedAt = t
				}
				continue
			}
		}

		var entry Entry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}

		if entry.Type == EntryMessage {
			info.MessageCount++
		}
		if entry.Type == EntrySessionInfo {
			var meta map[string]string
			if json.Unmarshal(entry.Data, &meta) == nil {
				if v, ok := meta["name"]; ok {
					info.Name = v
				}
			}
		}
		if entry.Type == EntryLabel {
			var ld LabelData
			if json.Unmarshal(entry.Data, &ld) == nil && ld.Label != "" {
				info.Label = ld.Label
			}
		}
	}

	return info
}

// MessagesFromEntries rebuilds a Message slice from entries (flat, no tree awareness).
func MessagesFromEntries(entries []Entry) []agentcore.Message {
	var msgs []agentcore.Message
	for _, e := range entries {
		if e.Type != EntryMessage {
			continue
		}
		var msg agentcore.Message
		if json.Unmarshal(e.Data, &msg) == nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// ---------------------------------------------------------------------------
// ID generator
// ---------------------------------------------------------------------------

func (m *Manager) generateID() string {
	m.idMu.Lock()
	defer m.idMu.Unlock()
	m.idCounter++
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), m.idCounter)
}

func generateID() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), 1)
}
