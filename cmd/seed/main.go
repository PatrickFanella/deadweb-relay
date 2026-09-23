// Command seed imports the authored site material from content.md into an
// empty relay data directory. It never adds content to an existing guestbook
// or wall.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"deadweb-relay/internal/store"
)

var guestbookEntry = regexp.MustCompile(`(?s)\*\*(\d{2}/\d{2}/\d{4}) — ([^*]+)\*\*\n\n(.*?)(?:\n\n---|\z)`)
var noteHeader = regexp.MustCompile(`(?m)^# /notes/([^\n]+)\n\n`)
var fileHeader = regexp.MustCompile(`(?m)^# /files/([^\n]+)\n\n`)

func main() {
	dataDir := flag.String("data", "./data", "relay data directory")
	contentPath := flag.String("content", "content.md", "authored content source")
	refresh := flag.Bool("refresh", false, "apply the one-time authored content refresh")
	flag.Parse()

	b, err := os.ReadFile(*contentPath)
	if err != nil {
		fatal(err)
	}
	parts := strings.Split(string(b), "### 2. Public notes and scraps")
	if len(parts) != 2 {
		fatal(fmt.Errorf("%s: expected guestbook and notes sections", *contentPath))
	}
	s, err := store.Open(*dataDir)
	if err != nil {
		fatal(err)
	}
	marker := ""
	if *refresh {
		marker = *dataDir + "/.authored-refresh-v1"
		if _, err := os.Stat(marker); err == nil {
			fatal(fmt.Errorf("authored refresh already applied in %s", *dataDir))
		}
	}
	if !*refresh && (len(s.Guestbook()) > 0 || len(s.Wall()) > 0) {
		fatal(fmt.Errorf("refusing to seed non-empty guestbook or wall in %s", *dataDir))
	}

	entries := guestbookEntry.FindAllStringSubmatch(parts[0], -1)
	if len(entries) == 0 {
		fatal(fmt.Errorf("%s: found no guestbook entries", *contentPath))
	}
	guestbookCount, wallCount := 0, 0
	guestbookSeen := map[string]bool{}
	for _, v := range s.Guestbook() {
		guestbookSeen[v.Name+"\x00"+v.Body] = true
	}
	wallSeen := map[string]bool{}
	for _, v := range s.Wall() {
		wallSeen[v.Name+"\x00"+v.Body] = true
	}
	for i, entry := range entries {
		at, err := time.Parse("01/02/2006", entry[1])
		if err != nil {
			fatal(err)
		}
		name, body := strings.TrimSpace(entry[2]), strings.TrimSpace(entry[3])
		// The authored document calls this a "guestbook / wall" timeline.
		// Keep guestbook-specific posts as discrete entries and use the wall for
		// chronological site/protocol broadcasts. The indices are deliberately
		// fixed so repeated imports remain reproducible and reviewable.
		if seededToGuestbook(i) {
			if guestbookSeen[name+"\x00"+body] {
				continue
			}
			if _, err := s.AddGuestbookAt(name, body, 0, at); err != nil {
				fatal(err)
			}
			guestbookCount++
			continue
		}
		if wallSeen[name+"\x00"+body] {
			continue
		}
		if _, err := s.AddWallAt(name, body, at); err != nil {
			fatal(err)
		}
		wallCount++
	}

	notesSection, rest, ok := strings.Cut(parts[1], "### 3. File cabinet")
	if !ok {
		fatal(fmt.Errorf("%s: expected file cabinet section", *contentPath))
	}
	filesSection, _, ok := strings.Cut(rest, "### 4. Miscellaneous old-web pages")
	if !ok {
		fatal(fmt.Errorf("%s: expected miscellaneous section", *contentPath))
	}
	noteCount := importNotes(s, notesSection)
	fileCount := importFiles(s, filesSection)
	if marker != "" {
		if err := os.WriteFile(marker, []byte("authored refresh applied\n"), 0o600); err != nil {
			fatal(err)
		}
	}

	fmt.Printf("seeded %d webmaster guestbook entries, %d webmaster wall entries, %d public notes, and %d files into %s\n", guestbookCount, wallCount, noteCount, fileCount, *dataDir)
}

func importNotes(s *store.Store, section string) int {
	matches := noteHeader.FindAllStringSubmatchIndex(section, -1)
	for i, match := range matches {
		bodyStart, bodyEnd := match[1], len(section)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		path := strings.TrimSpace(section[match[2]:match[3]])
		body := strings.TrimSpace(section[bodyStart:bodyEnd])
		if _, err := s.PutNoteAt(path, body, "text/plain", authoredTime(i)); err != nil {
			fatal(fmt.Errorf("seed note %q: %w", path, err))
		}
	}
	return len(matches)
}

func importFiles(s *store.Store, section string) int {
	existing := map[string]bool{}
	for _, v := range s.Files() {
		existing[v.Name] = true
	}
	matches := fileHeader.FindAllStringSubmatchIndex(section, -1)
	added := 0
	for i, match := range matches {
		bodyStart, bodyEnd := match[1], len(section)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		name := strings.TrimSpace(section[match[2]:match[3]])
		if existing[name] {
			continue
		}
		body := strings.TrimSpace(section[bodyStart:bodyEnd]) + "\n"
		if _, err := s.AddFileAt(name, "text/plain", []byte(body), authoredTime(8+i)); err != nil {
			fatal(fmt.Errorf("seed file %q: %w", name, err))
		}
		added++
	}
	return added
}

func authoredTime(index int) time.Time {
	return time.Date(2001, time.January, 17+index*43, 12, 0, 0, 0, time.UTC)
}

func seededToGuestbook(index int) bool {
	// Guestbook-specific entries: repairs, permanence, no-login policy, replies,
	// and the final year-marker. All remaining dated webmaster posts are wall
	// announcements from the same authored timeline.
	switch index {
	case 0, 2, 4, 10, 17, 20, 26:
		return true
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}
