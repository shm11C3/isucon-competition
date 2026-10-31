package isureal

import (
	"sync"
	"time"
)

// loginRejectTTL bounds staleness when initialize frees a username. A secret key
// hash never changes for a user, so a hit can only confirm a wrong key.
const loginRejectTTL = 2 * time.Second
const loginRejectMaxEntries = 100000

type loginRejectEntry struct {
	secretKeyHash string
	expiresAt     time.Time
}

type loginRejectStore struct {
	sync.RWMutex
	entries map[string]loginRejectEntry
}

var loginRejectCache = &loginRejectStore{entries: map[string]loginRejectEntry{}}

func (s *loginRejectStore) get(username string) (string, bool) {
	s.RLock()
	entry, ok := s.entries[username]
	s.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.secretKeyHash, true
}

func (s *loginRejectStore) put(username, secretKeyHash string) {
	s.Lock()
	defer s.Unlock()
	if len(s.entries) >= loginRejectMaxEntries {
		s.entries = map[string]loginRejectEntry{}
	}
	s.entries[username] = loginRejectEntry{secretKeyHash: secretKeyHash, expiresAt: time.Now().Add(loginRejectTTL)}
}
