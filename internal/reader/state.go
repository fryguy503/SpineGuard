package reader

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type savedState struct {
	State     ReadingState `json:"state"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

func cloneState(s ReadingState) ReadingState {
	s.Bookmarks = append([]Bookmark{}, s.Bookmarks...)
	return s
}
func validState(s ReadingState) error {
	if len(s.Location) > 4096 || !utf8.ValidString(s.Location) || strings.ContainsRune(s.Location, 0) || len(s.Bookmarks) > 200 {
		return errors.New("reading position or bookmark limit exceeded")
	}
	seen := map[string]bool{}
	for _, b := range s.Bookmarks {
		if b.ID == "" || seen[b.ID] || len(b.ID) > 100 || b.Location == "" || len(b.Location) > 4096 || len(b.Label) > 300 || len(b.Note) > 4000 || len(b.CreatedAt) > 80 {
			return errors.New("invalid bookmark or bookmark limit exceeded")
		}
		seen[b.ID] = true
		for _, v := range []string{b.ID, b.Location, b.Label, b.Note, b.CreatedAt} {
			if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
				return errors.New("bookmark contains invalid text")
			}
		}
	}
	return nil
}
func (m *Manager) loadStates() {
	if m.dir == "" {
		return
	}
	_ = m.refreshStates(context.Background())
}

func readStates(dir string) (map[string]savedState, error) {
	f, e := os.Open(filepath.Join(dir, "reader-state.json"))
	if os.IsNotExist(e) {
		return map[string]savedState{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if e != nil {
		return nil, e
	}
	var states map[string]savedState
	if len(data) > 4<<20 || json.Unmarshal(data, &states) != nil || len(states) > 200 {
		return nil, errors.New("saved reading state cannot be read; its existing file has been preserved")
	}
	if states == nil {
		states = map[string]savedState{}
	}
	for key, value := range states {
		if len(key) != 64 || validState(value.State) != nil {
			delete(states, key)
		}
	}
	return states, nil
}

// The caller owns m.mu, or is constructing a Manager not yet published.
func (m *Manager) refreshStates(ctx context.Context) error {
	if m.dir == "" {
		return nil
	}
	unlock, err := lockState(ctx, m.dir)
	if err != nil {
		return err
	}
	defer unlock()
	states, err := readStates(m.dir)
	if err == nil {
		m.states = states
	}
	return err
}
func (m *Manager) SaveState(id string, state ReadingState) error {
	if e := validState(state); e != nil {
		return e
	}
	state = cloneState(state)
	s, e := m.get(id)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.check(); e != nil {
		return e
	}
	if e = s.ctx.Err(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != s {
		return errors.New("reader session expired")
	}
	unlock, e := lockState(s.ctx, m.dir)
	if e != nil {
		return e
	}
	defer unlock()
	current := m.states
	if m.dir != "" {
		if current, e = readStates(m.dir); e != nil {
			return e
		}
	}
	updated := make(map[string]savedState, len(current)+1)
	for k, v := range current {
		updated[k] = v
	}
	updated[s.book.BookKey] = savedState{state, time.Now().UTC()}
	if len(updated) > 200 {
		keys := make([]string, 0, len(updated))
		for k := range updated {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return updated[keys[i]].UpdatedAt.Before(updated[keys[j]].UpdatedAt) })
		for len(updated) > 200 {
			delete(updated, keys[0])
			keys = keys[1:]
		}
	}
	data, e := json.Marshal(updated)
	if e != nil {
		return e
	}
	if len(data) > 4<<20 {
		return errors.New("saved reading state limit exceeded")
	}
	if m.dir != "" {
		if e = os.MkdirAll(m.dir, 0700); e != nil {
			return e
		}
		f, err := os.CreateTemp(m.dir, ".reader-state-*.tmp")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, e = f.Write(data); e != nil {
			f.Close()
			return e
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if e = os.Rename(name, filepath.Join(m.dir, "reader-state.json")); e != nil {
			return e
		}
	}
	m.states = updated
	s.book.State = state
	return nil
}
