package self

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const stateFileName = "self_state.json"

// SelfState is the persistent self-model.
type SelfState struct {
	Identity  Identity  `json:"identity"`
	Persona   Persona   `json:"persona"`
	UpdatedAt time.Time `json:"updated_at"`

	mu   sync.RWMutex
	path string
}

// New creates a SelfState with defaults, bound to a file path inside dataDir.
func New(dataDir string) *SelfState {
	return &SelfState{
		Identity:  DefaultIdentity(),
		Persona:   DefaultPersona(),
		UpdatedAt: time.Now().UTC(),
		path:      filepath.Join(dataDir, stateFileName),
	}
}

// Load reads the state from disk. If the file doesn't exist, returns defaults.
func Load(dataDir string) (*SelfState, error) {
	s := New(dataDir)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read self state: %w", err)
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse self state: %w", err)
	}
	return s, nil
}

// Save writes the state to disk atomically.
func (s *SelfState) Save() error {
	s.mu.Lock()
	s.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("marshal self state: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "self-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename self state: %w", err)
	}
	return nil
}

// GetIdentity returns a copy of the current identity.
func (s *SelfState) GetIdentity() Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Identity
}

// SetIdentity replaces the identity.
func (s *SelfState) SetIdentity(id Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Identity = id
}

// GetPersona returns a copy of the current persona.
func (s *SelfState) GetPersona() Persona {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Persona
}

// ApplyLessons adjusts persona from lessons and returns the updated persona.
func (s *SelfState) ApplyLessons(lessons []Lesson) Persona {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Persona = s.Persona.Adjust(lessons)
	return s.Persona
}
