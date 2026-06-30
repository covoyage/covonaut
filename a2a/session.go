package a2a

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Session Manager
// ---------------------------------------------------------------------------

// Session represents a multi-turn conversation context.
type Session struct {
	ID        string
	Tasks     []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionManager manages active sessions and their associated tasks.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// GetOrCreate returns an existing session or creates a new one.
func (sm *SessionManager) GetOrCreate(sessionID string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[sessionID]; ok {
		session.UpdatedAt = time.Now()
		return session
	}

	now := time.Now()
	session := &Session{
		ID:        sessionID,
		Tasks:     []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	sm.sessions[sessionID] = session
	return session
}

// Get retrieves a session by ID. Returns nil if not found.
func (sm *SessionManager) Get(sessionID string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

// AddTask associates a task with a session.
func (sm *SessionManager) AddTask(sessionID, taskID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		now := time.Now()
		session = &Session{
			ID:        sessionID,
			Tasks:     []string{},
			CreatedAt: now,
			UpdatedAt: now,
		}
		sm.sessions[sessionID] = session
	}

	// Avoid duplicates
	for _, id := range session.Tasks {
		if id == taskID {
			return
		}
	}
	session.Tasks = append(session.Tasks, taskID)
	session.UpdatedAt = time.Now()
}

// GetTasks returns all task IDs associated with a session.
func (sm *SessionManager) GetTasks(sessionID string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil
	}
	result := make([]string, len(session.Tasks))
	copy(result, session.Tasks)
	return result
}

// Delete removes a session.
func (sm *SessionManager) Delete(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// List returns all active session IDs.
func (sm *SessionManager) List() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	return ids
}

// PurgeStale removes sessions that haven't been updated since the cutoff time.
func (sm *SessionManager) PurgeStale(cutoff time.Time) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	count := 0
	for id, session := range sm.sessions {
		if session.UpdatedAt.Before(cutoff) {
			delete(sm.sessions, id)
			count++
		}
	}
	return count
}
