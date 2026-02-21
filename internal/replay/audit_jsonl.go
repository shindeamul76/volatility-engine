package replay

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

type Auditor struct {
	mu sync.Mutex
	w  *bufio.Writer
	f  *os.File
}

func NewAuditor(path string) (*Auditor, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return nil, err
	}
	return &Auditor{w: bufio.NewWriterSize(f, 1<<20), f: f}, nil
}

func (a *Auditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.w.Flush()
	return a.f.Close()
}

func (a *Auditor) Append(event any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := a.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return a.w.Flush()
}
