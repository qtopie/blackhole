package nfs

import (
	"crypto/rand"
	"sync"
	"time"
)

// SessionSlot tracks the sequence number and state of a single slot within an NFSv4.1/4.2 session.
type SessionSlot struct {
	SeqID uint32
}

// Session represents an NFSv4.1/4.2 session instance.
type Session struct {
	SessionID  [16]byte
	ClientID   uint64
	Flags      uint32
	MaxSlots   uint32
	CreatedAt  time.Time
	LastAccess time.Time

	mu    sync.Mutex
	Slots map[uint32]*SessionSlot
}

// SessionManager manages active NFSv4.1/4.2 sessions according to RFC 5661.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[[16]byte]*Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[[16]byte]*Session),
	}
}

// CreateSession allocates a new 16-byte session identifier and initializes session state.
func (sm *SessionManager) CreateSession(clientID uint64, flags uint32, maxSlots uint32) *Session {
	if maxSlots == 0 || maxSlots > 64 {
		maxSlots = 64
	}

	var sessionID [16]byte
	for {
		_, _ = rand.Read(sessionID[:])
		sm.mu.RLock()
		_, exists := sm.sessions[sessionID]
		sm.mu.RUnlock()
		if !exists && sessionID != [16]byte{} {
			break
		}
	}

	sess := &Session{
		SessionID:  sessionID,
		ClientID:   clientID,
		Flags:      flags,
		MaxSlots:   maxSlots,
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
		Slots:      make(map[uint32]*SessionSlot),
	}

	sm.mu.Lock()
	sm.sessions[sessionID] = sess
	sm.mu.Unlock()

	return sess
}

// GetSession retrieves an active session by its 16-byte session ID.
func (sm *SessionManager) GetSession(sessionID [16]byte) (*Session, bool) {
	sm.mu.RLock()
	sess, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()
	return sess, exists
}

// DestroySession releases a session and frees its resources.
func (sm *SessionManager) DestroySession(sessionID [16]byte) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, exists := sm.sessions[sessionID]; exists {
		delete(sm.sessions, sessionID)
		return true
	}
	return false
}

// ProcessSequence validates and updates slot sequence numbers according to RFC 5661 Section 2.10.6.
func (sm *SessionManager) ProcessSequence(sessionID [16]byte, seqID uint32, slotID uint32, highestSlot uint32) (status uint32, targetHighest uint32) {
	sm.mu.RLock()
	sess, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !exists {
		return NFS4ERRBADSESSION, 0
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.LastAccess = time.Now()

	maxSlots := sess.MaxSlots
	if slotID >= maxSlots {
		return NFS4ERRBADSLOT, maxSlots - 1
	}

	slot, ok := sess.Slots[slotID]
	if !ok {
		// First request on this slot
		sess.Slots[slotID] = &SessionSlot{SeqID: seqID}
		return NFS4OK, maxSlots - 1
	}

	// Normal sequence increment
	if seqID == slot.SeqID+1 {
		slot.SeqID = seqID
		return NFS4OK, maxSlots - 1
	}

	// Exactly-Once Replay
	if seqID == slot.SeqID {
		return NFS4OK, maxSlots - 1
	}

	// Sequence wrap-around
	if seqID == 1 && slot.SeqID == 0xFFFFFFFF {
		slot.SeqID = seqID
		return NFS4OK, maxSlots - 1
	}

	// Misordered sequence error
	return NFS4ERRSEQMISORDERED, maxSlots - 1
}
