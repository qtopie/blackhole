package nfs

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"sync"
)

const (
	LockTypeRead   uint32 = 1
	LockTypeWrite  uint32 = 2
	LockTypeReadW  uint32 = 3
	LockTypeWriteW uint32 = 4
)

type ByteRangeLock struct {
	Type     uint32
	Offset   uint64
	Length   uint64
	ClientID uint64
	Owner    string
	StateID  [16]byte
	SeqID    uint32
}

type LockOwnerInfo struct {
	ClientID uint64
	Owner    string
}

type LockManager struct {
	mu           sync.RWMutex
	locks        map[string][]*ByteRangeLock // relative path -> list of active locks
	stateToOwner map[[16]byte]LockOwnerInfo
}

func NewLockManager() *LockManager {
	return &LockManager{
		locks:        make(map[string][]*ByteRangeLock),
		stateToOwner: make(map[[16]byte]LockOwnerInfo),
	}
}

func rangesOverlap(off1, len1, off2, len2 uint64) bool {
	var end1, end2 uint64
	if len1 == 0 || len1 == ^uint64(0) || off1+len1 < off1 {
		end1 = ^uint64(0)
	} else {
		end1 = off1 + len1
	}

	if len2 == 0 || len2 == ^uint64(0) || off2+len2 < off2 {
		end2 = ^uint64(0)
	} else {
		end2 = off2 + len2
	}

	return off1 < end2 && off2 < end1
}

// CheckConflict tests if a requested lock conflicts with an existing lock.
func (lm *LockManager) CheckConflict(path string, lockType uint32, offset, length uint64, clientID uint64, owner string) *ByteRangeLock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	active := lm.locks[path]
	for _, l := range active {
		if !rangesOverlap(offset, length, l.Offset, l.Length) {
			continue
		}
		// Same owner does not conflict with itself
		if l.ClientID == clientID && l.Owner == owner {
			continue
		}
		// Shared read locks do not conflict
		isRead1 := (lockType == LockTypeRead || lockType == LockTypeReadW)
		isRead2 := (l.Type == LockTypeRead || l.Type == LockTypeReadW)
		if isRead1 && isRead2 {
			continue
		}
		// Conflict found!
		return l
	}
	return nil
}

// AcquireLock attempts to acquire a byte-range lock.
func (lm *LockManager) AcquireLock(path string, lockType uint32, offset, length uint64, isNewOwner bool, clientID uint64, owner string, existingStateID [16]byte) (stateID [16]byte, conflict *ByteRangeLock) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	effectiveCID := clientID
	effectiveOwner := owner

	if !isNewOwner {
		for sid, info := range lm.stateToOwner {
			if bytes.Equal(sid[4:], existingStateID[4:]) {
				effectiveCID = info.ClientID
				effectiveOwner = info.Owner
				break
			}
		}
	}

	active := lm.locks[path]
	for _, l := range active {
		if !rangesOverlap(offset, length, l.Offset, l.Length) {
			continue
		}
		// Same owner or same stateid other does not conflict with itself
		sameOwner := (effectiveOwner != "" && l.ClientID == effectiveCID && l.Owner == effectiveOwner)
		sameState := (!isNewOwner && bytes.Equal(l.StateID[4:], existingStateID[4:]))
		if sameOwner || sameState {
			continue
		}
		isRead1 := (lockType == LockTypeRead || lockType == LockTypeReadW)
		isRead2 := (l.Type == LockTypeRead || l.Type == LockTypeReadW)
		if isRead1 && isRead2 {
			continue
		}
		return stateID, l // Conflict
	}

	var sid [16]byte
	if !isNewOwner && existingStateID != [16]byte{} {
		copy(sid[:], existingStateID[:])
		currSeq := binary.BigEndian.Uint32(sid[0:4])
		binary.BigEndian.PutUint32(sid[0:4], currSeq+1)
	} else {
		binary.BigEndian.PutUint32(sid[0:4], 1)
		_, _ = rand.Read(sid[4:16])
	}

	newLock := &ByteRangeLock{
		Type:     lockType,
		Offset:   offset,
		Length:   length,
		ClientID: effectiveCID,
		Owner:    effectiveOwner,
		StateID:  sid,
		SeqID:    binary.BigEndian.Uint32(sid[0:4]),
	}

	lm.locks[path] = append(lm.locks[path], newLock)
	if effectiveOwner != "" {
		lm.stateToOwner[sid] = LockOwnerInfo{ClientID: effectiveCID, Owner: effectiveOwner}
	}
	return sid, nil
}

// Unlock releases or shrinks locks matching the given range and owner.
func (lm *LockManager) Unlock(path string, offset, length uint64, stateID [16]byte) ([16]byte, bool) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	var effectiveCID uint64
	var effectiveOwner string
	for sid, info := range lm.stateToOwner {
		if bytes.Equal(sid[4:], stateID[4:]) {
			effectiveCID = info.ClientID
			effectiveOwner = info.Owner
			break
		}
	}

	active := lm.locks[path]
	var updated []*ByteRangeLock

	unlockEnd := offset + length
	if length == 0 || length == ^uint64(0) || offset+length < offset {
		unlockEnd = ^uint64(0)
	}

	for _, l := range active {
		matchesOwner := (effectiveOwner != "" && l.ClientID == effectiveCID && l.Owner == effectiveOwner) || bytes.Equal(l.StateID[4:], stateID[4:])
		if !matchesOwner {
			updated = append(updated, l)
			continue
		}

		if !rangesOverlap(offset, length, l.Offset, l.Length) {
			updated = append(updated, l)
			continue
		}

		lockEnd := l.Offset + l.Length
		if l.Length == 0 || l.Length == ^uint64(0) || l.Offset+l.Length < l.Offset {
			lockEnd = ^uint64(0)
		}

		// Full removal
		if offset <= l.Offset && unlockEnd >= lockEnd {
			continue
		}

		// Partial shrink from head
		if offset <= l.Offset && unlockEnd < lockEnd {
			l.Offset = unlockEnd
			l.Length = lockEnd - unlockEnd
			updated = append(updated, l)
		} else if offset > l.Offset && unlockEnd >= lockEnd {
			// Partial shrink from tail
			l.Length = offset - l.Offset
			updated = append(updated, l)
		} else {
			// Split middle
			tail := &ByteRangeLock{
				Type:     l.Type,
				Offset:   unlockEnd,
				Length:   lockEnd - unlockEnd,
				ClientID: l.ClientID,
				Owner:    l.Owner,
				StateID:  l.StateID,
				SeqID:    l.SeqID,
			}
			l.Length = offset - l.Offset
			updated = append(updated, l, tail)
		}
	}

	if len(updated) == 0 {
		delete(lm.locks, path)
	} else {
		lm.locks[path] = updated
	}

	var sid [16]byte
	copy(sid[:], stateID[:])
	currSeq := binary.BigEndian.Uint32(sid[0:4])
	binary.BigEndian.PutUint32(sid[0:4], currSeq+1)

	if effectiveOwner != "" {
		lm.stateToOwner[sid] = LockOwnerInfo{ClientID: effectiveCID, Owner: effectiveOwner}
	}
	return sid, true
}

// ReleaseLockOwner purges all active locks held by a lock owner.
func (lm *LockManager) ReleaseLockOwner(clientID uint64, owner string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for path, list := range lm.locks {
		var remaining []*ByteRangeLock
		for _, l := range list {
			if l.ClientID == clientID && l.Owner == owner {
				delete(lm.stateToOwner, l.StateID)
				continue
			}
			remaining = append(remaining, l)
		}
		if len(remaining) == 0 {
			delete(lm.locks, path)
		} else {
			lm.locks[path] = remaining
		}
	}
}
