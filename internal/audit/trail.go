package audit

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type Trail struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

func NewTrail(path string) (*Trail, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Trail{file: file, enc: json.NewEncoder(file)}, nil
}

func (t *Trail) Log(event Event) error {
	if t == nil || t.file == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return t.enc.Encode(event)
}

func (t *Trail) Close() error {
	if t == nil || t.file == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.file.Close()
}
