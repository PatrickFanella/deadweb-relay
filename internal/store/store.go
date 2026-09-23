package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GuestbookEntry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	ReplyTo int64     `json:"reply_to,omitempty"`
}

type WallEntry struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	Name string    `json:"name"`
	Body string    `json:"body"`
}

type Note struct {
	Path        string    `json:"path"`
	Time        time.Time `json:"time"`
	Body        string    `json:"body"`
	ContentType string    `json:"content_type"`
	Version     int64     `json:"version"`
}

type FileMeta struct {
	ID          int64     `json:"id"`
	Time        time.Time `json:"time"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	DiskName    string    `json:"disk_name"`
}

type Event struct {
	Time       time.Time `json:"time"`
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Referer    string    `json:"referer,omitempty"`
	Accept     string    `json:"accept,omitempty"`
	RemoteHash string    `json:"remote_hash,omitempty"`
	WriteType  string    `json:"write_type,omitempty"`
	ObjectID   string    `json:"object_id,omitempty"`
}

type SearchResult struct {
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Title   string    `json:"title"`
	Snippet string    `json:"snippet"`
	URL     string    `json:"url"`
}

type Store struct {
	mu        sync.RWMutex
	dir       string
	guestbook []GuestbookEntry
	wall      []WallEntry
	notes     map[string]Note
	files     []FileMeta
	nextID    int64
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o750); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, notes: map[string]Note{}, nextID: time.Now().UnixNano() / 1e6}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	if err := loadJSONL(filepath.Join(s.dir, "guestbook.jsonl"), func(b []byte) error {
		var v GuestbookEntry
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.guestbook = append(s.guestbook, v)
		if v.ID >= s.nextID {
			s.nextID = v.ID + 1
		}
		return nil
	}); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, "wall.jsonl"), func(b []byte) error {
		var v WallEntry
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.wall = append(s.wall, v)
		if v.ID >= s.nextID {
			s.nextID = v.ID + 1
		}
		return nil
	}); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, "notes.jsonl"), func(b []byte) error {
		var v Note
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.notes[v.Path] = v
		if v.Version >= s.nextID {
			s.nextID = v.Version + 1
		}
		return nil
	}); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, "files.jsonl"), func(b []byte) error {
		var v FileMeta
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		s.files = append(s.files, v)
		if v.ID >= s.nextID {
			s.nextID = v.ID + 1
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func loadJSONL(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return sc.Err()
}

func appendJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) next() int64 { s.nextID++; return s.nextID }

func (s *Store) AddGuestbook(name, body string, replyTo int64) (GuestbookEntry, error) {
	return s.addGuestbook(name, body, replyTo, time.Now().UTC())
}

// AddGuestbookAt imports an authored historical entry with its supplied time.
// It is intended for one-time site bootstrap data, not public requests.
func (s *Store) AddGuestbookAt(name, body string, replyTo int64, at time.Time) (GuestbookEntry, error) {
	return s.addGuestbook(name, body, replyTo, at.UTC())
}

func (s *Store) addGuestbook(name, body string, replyTo int64, at time.Time) (GuestbookEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := GuestbookEntry{ID: s.next(), Time: at, Name: name, Body: body, ReplyTo: replyTo}
	if err := appendJSON(filepath.Join(s.dir, "guestbook.jsonl"), v); err != nil {
		return GuestbookEntry{}, err
	}
	s.guestbook = append(s.guestbook, v)
	return v, nil
}

func (s *Store) Guestbook() []GuestbookEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]GuestbookEntry(nil), s.guestbook...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// GuestbookPage returns newest-first entries for a one-based page. It avoids
// allocating and sorting the full guestbook for every browser request.
func (s *Store) GuestbookPage(page, perPage int) ([]GuestbookEntry, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.guestbook)
	start := (page - 1) * perPage
	if start >= total {
		return nil, total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	out := make([]GuestbookEntry, 0, end-start)
	for i := total - 1 - start; i >= total-end; i-- {
		out = append(out, s.guestbook[i])
	}
	return out, total
}

func (s *Store) GuestbookByID(id int64) (GuestbookEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.guestbook) - 1; i >= 0; i-- {
		if s.guestbook[i].ID == id {
			return s.guestbook[i], true
		}
	}
	return GuestbookEntry{}, false
}

func (s *Store) AddWall(name, body string) (WallEntry, error) {
	return s.addWall(name, body, time.Now().UTC())
}

// AddWallAt imports an authored historical wall entry with its supplied time.
// It is intended for one-time site bootstrap data, not public requests.
func (s *Store) AddWallAt(name, body string, at time.Time) (WallEntry, error) {
	return s.addWall(name, body, at.UTC())
}

func (s *Store) addWall(name, body string, at time.Time) (WallEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := WallEntry{ID: s.next(), Time: at, Name: name, Body: body}
	if err := appendJSON(filepath.Join(s.dir, "wall.jsonl"), v); err != nil {
		return WallEntry{}, err
	}
	s.wall = append(s.wall, v)
	return v, nil
}

func (s *Store) Wall() []WallEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]WallEntry(nil), s.wall...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func cleanNotePath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", errors.New("empty note path")
	}
	parts := strings.Split(p, "/")
	if len(parts) > 12 {
		return "", errors.New("path too deep")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid path")
		}
		if len(part) > 80 {
			return "", errors.New("path segment too long")
		}
		for _, r := range part {
			if !(r == '-' || r == '_' || r == '.' || r == '~' || r == '@' || r == '+' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
				return "", errors.New("unsupported character in path")
			}
		}
	}
	return strings.Join(parts, "/"), nil
}

func (s *Store) PutNote(path, body, contentType string) (Note, error) {
	return s.putNote(path, body, contentType, time.Now().UTC())
}

// PutNoteAt imports authored historical content at its supplied time.
func (s *Store) PutNoteAt(path, body, contentType string, at time.Time) (Note, error) {
	return s.putNote(path, body, contentType, at.UTC())
}

func (s *Store) putNote(path, body, contentType string, at time.Time) (Note, error) {
	p, err := cleanNotePath(path)
	if err != nil {
		return Note{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Note{Path: p, Time: at, Body: body, ContentType: contentType, Version: s.next()}
	if err := appendJSON(filepath.Join(s.dir, "notes.jsonl"), v); err != nil {
		return Note{}, err
	}
	s.notes[p] = v
	return v, nil
}

func (s *Store) Note(path string) (Note, bool) {
	p, err := cleanNotePath(path)
	if err != nil {
		return Note{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.notes[p]
	return v, ok
}

func (s *Store) Notes(prefix string) []Note {
	prefix = strings.Trim(prefix, "/")
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Note, 0, len(s.notes))
	for k, v := range s.notes {
		if prefix == "" || k == prefix || strings.HasPrefix(k, prefix+"/") {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		return "file.txt"
	}
	var b strings.Builder
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' || r == ' ' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	v := strings.TrimSpace(b.String())
	if v == "" {
		return "file.txt"
	}
	if len(v) > 120 {
		v = v[:120]
	}
	return v
}

func (s *Store) AddFile(name, contentType string, body []byte) (FileMeta, error) {
	return s.addFile(name, contentType, body, time.Now().UTC())
}

// AddFileAt imports an authored historical file at its supplied time.
func (s *Store) AddFileAt(name, contentType string, body []byte, at time.Time) (FileMeta, error) {
	return s.addFile(name, contentType, body, at.UTC())
}

func (s *Store) addFile(name, contentType string, body []byte, at time.Time) (FileMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next()
	name = sanitizeFilename(name)
	diskName := strconv.FormatInt(id, 10) + "_" + strings.ReplaceAll(name, " ", "_")
	path := filepath.Join(s.dir, "files", diskName)
	if err := os.WriteFile(path, body, 0o640); err != nil {
		return FileMeta{}, err
	}
	h := sha256.Sum256(body)
	v := FileMeta{ID: id, Time: at, Name: name, ContentType: contentType, Size: int64(len(body)), SHA256: hex.EncodeToString(h[:]), DiskName: diskName}
	if err := appendJSON(filepath.Join(s.dir, "files.jsonl"), v); err != nil {
		_ = os.Remove(path)
		return FileMeta{}, err
	}
	s.files = append(s.files, v)
	return v, nil
}

func (s *Store) Files() []FileMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]FileMeta(nil), s.files...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func (s *Store) File(id int64) (FileMeta, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.files {
		if v.ID == id {
			return v, filepath.Join(s.dir, "files", v.DiskName), true
		}
	}
	return FileMeta{}, "", false
}

func (s *Store) Search(q string) []SearchResult {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SearchResult
	match := func(s string) bool { return strings.Contains(strings.ToLower(s), q) }
	clip := func(s string) string {
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 180 {
			return s[:180] + "…"
		}
		return s
	}
	for _, v := range s.guestbook {
		if match(v.Name + " " + v.Body) {
			out = append(out, SearchResult{"guestbook", strconv.FormatInt(v.ID, 10), v.Time, v.Name, clip(v.Body), fmt.Sprintf("/guestbook/%d", v.ID)})
		}
	}
	for _, v := range s.wall {
		if match(v.Name + " " + v.Body) {
			out = append(out, SearchResult{"wall", strconv.FormatInt(v.ID, 10), v.Time, v.Name, clip(v.Body), "/wall/"})
		}
	}
	for _, v := range s.notes {
		if match(v.Path + " " + v.Body) {
			out = append(out, SearchResult{"note", v.Path, v.Time, v.Path, clip(v.Body), "/notes/" + v.Path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func (s *Store) AppendEvent(v Event) error {
	return appendJSON(filepath.Join(s.dir, "events.jsonl"), v)
}

func (s *Store) EventTail(limit int) ([]Event, error) {
	path := filepath.Join(s.dir, "events.jsonl")
	var events []Event
	err := loadJSONL(path, func(b []byte) error {
		var v Event
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		events = append(events, v)
		if len(events) > limit*3 {
			events = events[len(events)-limit*2:]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}
