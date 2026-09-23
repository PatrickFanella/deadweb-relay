package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deadweb-relay/internal/ratelimit"
	"deadweb-relay/internal/safety"
	"deadweb-relay/internal/store"
	webassets "deadweb-relay/web"
)

type app struct {
	store        *store.Store
	siteName     string
	baseURL      string
	adminToken   string
	hmacKey      []byte
	maxWrite     int64
	writeLimiter *ratelimit.Limiter
	tHome        *template.Template
	tList        *template.Template
	tPlain       *template.Template
}

type baseData struct {
	Title    string
	SiteName string
	Now      time.Time
	Count    int64
	Heading  string
	Intro    string
	FormHTML template.HTML
	Items    []template.HTML
	PageNav  template.HTML
	BodyHTML template.HTML
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(b)
}

type ctxKey string

const requestIDKey ctxKey = "request-id"

func main() {
	dataDir := getenv("DATA_DIR", "./data")
	s, err := store.Open(dataDir)
	if err != nil {
		log.Fatal(err)
	}

	parse := func(page string) *template.Template {
		t := template.New("base").Funcs(template.FuncMap{"safe": func(s string) template.HTML { return template.HTML(s) }})
		b, err := fs.ReadFile(webassets.FS, "templates/base.html")
		if err != nil {
			log.Fatal(err)
		}
		p, err := fs.ReadFile(webassets.FS, "templates/"+page)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := t.Parse(string(b)); err != nil {
			log.Fatal(err)
		}
		if _, err := t.Parse(string(p)); err != nil {
			log.Fatal(err)
		}
		return t
	}

	limit, _ := strconv.Atoi(getenv("WRITE_RATE_PER_HOUR", "60"))
	maxWrite, _ := strconv.ParseInt(getenv("MAX_WRITE_BYTES", "65536"), 10, 64)
	if maxWrite <= 0 {
		maxWrite = 65536
	}
	a := &app{
		store:        s,
		siteName:     getenv("SITE_NAME", "Patrick's WWW Page"),
		baseURL:      strings.TrimRight(getenv("BASE_URL", "http://localhost:8080"), "/"),
		adminToken:   os.Getenv("ADMIN_TOKEN"),
		hmacKey:      []byte(getenv("OBSERVATORY_HMAC_KEY", "local-dev-key")),
		maxWrite:     maxWrite,
		writeLimiter: ratelimit.New(limit, time.Hour),
		tHome:        parse("home.html"), tList: parse("list.html"), tPlain: parse("plainpage.html"),
	}

	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(webassets.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", a.route)

	addr := getenv("ADDR", ":8080")
	log.Printf("relay listening on %s", addr)
	server := &http.Server{Addr: addr, Handler: a.middleware(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		log.Print("relay shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("relay shutdown: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func getenv(k, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fallback
}

func (a *app) route(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		a.handleOptions(w, r)
		return
	}
	switch {
	case r.URL.Path == "/healthz":
		a.health(w, r)
	case r.URL.Path == "/":
		a.home(w, r)
	case r.URL.Path == "/index.txt":
		a.indexText(w, r)
	case r.URL.Path == "/reference/":
		a.simplePage(w, r, "reference shelf", "reference shelf", `<p>Small reference sheets by the webmaster, published September 22, 2026. Plain text, no downloads to install.</p><ul><li><a href="/reference/http-relay.txt">A small writable website using ordinary HTTP</a></li><li><a href="/reference/plain-text.txt">Keeping a useful plain-text notebook</a></li><li><a href="/reference/text-formats.txt">CSV, JSON, and line-oriented JSON</a></li><li><a href="/reference/backup-checklist.txt">Small website backup checklist</a></li></ul><p class="small"><a href="/reference/changes.txt">Dated changes and authorship</a></p>`)
	case strings.HasPrefix(r.URL.Path, "/reference/"):
		a.reference(w, r)
	case r.URL.Path == "/about.html":
		a.simplePage(w, r, "about", "about this page", `<p>Personal homepage, scratchpad, guestbook, and file cabinet. Some of it is old and some of it only looks old.</p><p>Everything public here should be treated as public. Please do not leave passwords, private keys, access tokens, or personal information.</p>`)
	case r.URL.Path == "/links.html":
		a.simplePage(w, r, "links", "cool links", `<p>Things I still like about the web:</p><ul><li>pages that work in Lynx</li><li>personal homepages</li><li>weird little utilities</li><li>public FTP directories</li><li>RSS</li><li>view source</li><li>URLs that keep working</li><li>websites maintained by one person for an unreasonable number of years</li></ul><p>If you run a crawler please don't hammer the server.</p>`)
	case r.URL.Path == "/music.html":
		a.simplePage(w, r, "music", "music page", `<p>Under construction. MIDI file collection lost in a hard-drive incident.</p>`)
	case r.URL.Path == "/my-computer.html":
		a.simplePage(w, r, "my computer", "computer stuff", `<pre>CPU: fast enough\nRAM: some\nMODEM: sadly no longer makes the noise\nOS: unix-shaped</pre>`)
	case r.URL.Path == "/webring/" || r.URL.Path == "/webring/index.html":
		a.simplePage(w, r, "webring", "local webring", `<p>&lt;&lt; <a href="/links.html">previous</a> | <a href="/">random</a> | <a href="/archive/">next</a> &gt;&gt;</p><p class="small">If you run a tiny weird website, congratulations, you are now spiritually in the ring.</p>`)
	case r.URL.Path == "/archive/":
		a.simplePage(w, r, "archive", "archive", `<pre>2001/\n2004/\nold/\nREADME.TXT</pre><p><a href="/archive/README.TXT">README.TXT</a> · <a href="/archive/old-server-notes.txt">old server notes</a> · <a href="/archive/2004-ideas.txt">2004 ideas</a></p>`)
	case r.URL.Path == "/archive/README.TXT":
		a.text(w, `ARCHIVE README\n==============\n\nOld things are kept because deleting things feels rude.\nCurrent scratch notes live under /notes/.\n`)
	case r.URL.Path == "/archive/old-server-notes.txt":
		a.text(w, "I used to think every website needed a database and a user system.\n\nNow I mostly want:\n\n1. somewhere to put text\n2. a permanent address for it\n3. a way for something else to retrieve it\n\neverything after that seems optional\n")
	case r.URL.Path == "/archive/2004-ideas.txt":
		a.text(w, "possible features:\n\n[ ] thumbnails\n[ ] hit counter graphs\n[ ] make logo smaller\n[ ] music page\n[ ] weather thing?\n[ ] guestbook smileys\n[ ] fix netscape css\n[x] raw text output\n[ ] signatures?\n[x] search\n[ ] upload pictures\n[ ] inbox?\n[ ] new background\n[ ] subscriptions?\n[ ] uptime page\n[x] permanent guestbook urls\n[x] json\n[ ] some way to ask what methods a page supports\n")
	case r.URL.Path == "/tmp/":
		a.simplePage(w, r, "tmp", "/tmp", `<p>Public scratch area. Things here may be overwritten.</p><p><a href="/tmp/test.txt">test.txt</a> · <a href="/tmp/hello.txt">hello.txt</a></p><p>The maintained writable namespace is <a href="/notes/">/notes/</a>.</p>`)
	case r.URL.Path == "/tmp/test.txt":
		a.text(w, "test\n\nif you are reading this, /tmp/ apparently still isn't temporary\n")
	case r.URL.Path == "/tmp/hello.txt":
		a.text(w, "hello whoever finds this\n\nnothing interesting here\n")
	case r.URL.Path == "/README.TXT":
		a.text(w, a.readmeText())
	case r.URL.Path == "/help.txt":
		a.text(w, a.helpText())
	case r.URL.Path == "/humans.txt":
		a.text(w, "/* humans.txt */\nMade with Go, plain HTML, questionable nostalgia, and no client-side framework.\n")
	case r.URL.Path == "/indexnow.txt":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		content, err := fs.ReadFile(webassets.FS, "static/indexnow.txt")
		if err != nil {
			http.Error(w, "verification unavailable", 500)
			return
		}
		a.text(w, string(content))
	case r.URL.Path == "/robots.txt":
		a.robots(w, r)
	case r.URL.Path == "/sitemap.xml":
		a.sitemap(w, r)
	case r.URL.Path == "/manifest.json":
		a.manifest(w, r)
	case r.URL.Path == "/opensearch.xml":
		a.openSearch(w, r)
	case r.URL.Path == "/.well-known/host-meta":
		a.hostMeta(w, r)
	case r.URL.Path == "/.well-known/host-meta.json":
		a.hostMetaJSON(w, r)
	case r.URL.Path == "/.well-known/site-meta.json":
		a.siteMeta(w, r)
	case r.URL.Path == "/.well-known/webfinger":
		a.webfinger(w, r)
	case r.URL.Path == "/.well-known/nodeinfo":
		a.nodeInfoDiscovery(w, r)
	case r.URL.Path == "/nodeinfo/2.1":
		a.nodeInfo(w, r)
	case r.URL.Path == "/guestbook/" || r.URL.Path == "/cgi-bin/guestbook.cgi":
		a.guestbook(w, r)
	case strings.HasPrefix(r.URL.Path, "/guestbook/"):
		a.guestbookItem(w, r)
	case r.URL.Path == "/wall/" || r.URL.Path == "/wall.txt" || r.URL.Path == "/wall.json":
		a.wall(w, r)
	case r.URL.Path == "/notes/":
		a.notesIndex(w, r)
	case strings.HasPrefix(r.URL.Path, "/notes/"):
		a.note(w, r)
	case r.URL.Path == "/files/":
		a.files(w, r)
	case strings.HasPrefix(r.URL.Path, "/files/"):
		a.fileItem(w, r)
	case r.URL.Path == "/search":
		a.search(w, r)
	case r.URL.Path == "/feed.json":
		a.feedJSON(w, r)
	case r.URL.Path == "/feed.ndjson":
		a.feedNDJSON(w, r)
	case r.URL.Path == "/feed.atom":
		a.feedAtom(w, r)
	case r.URL.Path == "/__observatory":
		a.observatory(w, r)
	case r.URL.Path == "/__events.ndjson":
		a.events(w, r)
	default:
		a.notFound(w, r)
	}
}

func (a *app) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := randomID(8)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, rid))
		w.Header().Set("X-Request-ID", rid)
		w.Header().Add("Link", `</help.txt>; rel="help"`)
		w.Header().Add("Link", `</feed.atom>; rel="alternate"; type="application/atom+xml"`)
		w.Header().Add("Link", `</opensearch.xml>; rel="search"; type="application/opensearchdescription+xml"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = 200
		}
		if err := a.store.AppendEvent(store.Event{
			Time: time.Now().UTC(), RequestID: rid, Method: r.Method, Path: r.URL.RequestURI(), Status: status,
			DurationMS: time.Since(start).Milliseconds(), UserAgent: clip(r.UserAgent(), 500), Referer: clip(r.Referer(), 500), Accept: clip(r.Header.Get("Accept"), 300), RemoteHash: a.remoteHash(r),
		}); err != nil {
			log.Printf("append request event: %v", err)
		}
	})
}

func (a *app) remoteHash(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	m := hmac.New(sha256.New, a.hmacKey)
	_, _ = m.Write([]byte(host))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

func (a *app) logWrite(r *http.Request, typ, object string) {
	if err := a.store.AppendEvent(store.Event{Time: time.Now().UTC(), RequestID: fmt.Sprint(r.Context().Value(requestIDKey)), Method: r.Method, Path: r.URL.RequestURI(), Status: 200, RemoteHash: a.remoteHash(r), WriteType: typ, ObjectID: object}); err != nil {
		log.Printf("append write event: %v", err)
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (a *app) base(title string) baseData {
	return baseData{Title: title, SiteName: a.siteName, Now: time.Now(), Count: time.Now().Unix() % 100000000}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (a *app) render(w http.ResponseWriter, t *template.Template, d baseData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", d); err != nil {
		log.Printf("template: %v", err)
	}
}

func (a *app) home(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	a.render(w, a.tHome, a.base("home"))
}
func (a *app) indexText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	a.text(w, "PATRICK'S WWW PAGE\n==================\n/guestbook/\n/wall/\n/notes/\n/files/\n/reference/\n/search\n/help.txt\n")
}

func (a *app) reference(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/reference/")
	switch name {
	case "plain-text.txt", "text-formats.txt", "backup-checklist.txt", "changes.txt", "http-relay.txt":
		content, err := fs.ReadFile(webassets.FS, "reference/"+name)
		if err != nil {
			http.Error(w, "reference unavailable", 500)
			return
		}
		a.text(w, string(content))
	default:
		a.notFound(w, r)
	}
}

func (a *app) simplePage(w http.ResponseWriter, r *http.Request, title, heading, body string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	d := a.base(title)
	d.Heading = heading
	d.BodyHTML = template.HTML(body)
	a.render(w, a.tPlain, d)
}
func (a *app) text(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, s)
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (a *app) allowWrite(w http.ResponseWriter, r *http.Request) bool {
	if !a.writeLimiter.Allow(a.remoteHash(r)) {
		http.Error(w, "too many writes; try later", http.StatusTooManyRequests)
		return false
	}
	return true
}
func (a *app) readLimited(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxWrite)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	if kind, bad := safety.CredentialLike(string(b)); bad {
		http.Error(w, "content resembles a credential or private key ("+kind+") and was not stored", http.StatusUnprocessableEntity)
		return nil, false
	}
	return b, true
}

func accepts(r *http.Request, typ string) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), typ)
}
func wantsJSON(r *http.Request) bool {
	return r.URL.Query().Get("format") == "json" || accepts(r, "application/json") || strings.HasSuffix(r.URL.Path, ".json")
}
func wantsText(r *http.Request) bool {
	return r.URL.Query().Get("format") == "txt" || accepts(r, "text/plain") || strings.HasSuffix(r.URL.Path, ".txt")
}

func (a *app) guestbook(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Link", `</guestbook/?format=json>; rel="alternate"; type="application/json"`)
	w.Header().Add("Link", `</guestbook/?format=txt>; rel="alternate"; type="text/plain"`)
	if r.Method == http.MethodPost {
		if !a.allowWrite(w, r) {
			return
		}
		var name, body string
		var reply int64
		ct := r.Header.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			b, ok := a.readLimited(w, r)
			if !ok {
				return
			}
			var v struct {
				Name    string `json:"name"`
				Body    string `json:"body"`
				ReplyTo int64  `json:"reply_to"`
			}
			if json.Unmarshal(b, &v) != nil {
				http.Error(w, "bad json", 400)
				return
			}
			name, body, reply = v.Name, v.Body, v.ReplyTo
		} else {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			name = r.FormValue("name")
			body = r.FormValue("body")
			reply, _ = strconv.ParseInt(r.FormValue("reply_to"), 10, 64)
			if _, bad := safety.CredentialLike(body); bad {
				http.Error(w, "content resembles a credential or private key and was not stored", 422)
				return
			}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "anon"
		}
		body = strings.TrimSpace(body)
		if body == "" {
			http.Error(w, "empty message", 400)
			return
		}
		if len(name) > 80 || int64(len(body)) > a.maxWrite {
			http.Error(w, "too large", 413)
			return
		}
		v, err := a.store.AddGuestbook(name, body, reply)
		if err != nil {
			http.Error(w, "store error", 500)
			return
		}
		a.logWrite(r, "guestbook", strconv.FormatInt(v.ID, 10))
		if wantsJSON(r) {
			writeJSON(w, v)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/guestbook/%d", v.ID), http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, POST, OPTIONS")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const perPage = 50
	items, total := a.store.GuestbookPage(page, perPage)
	if wantsJSON(r) {
		writeJSON(w, items)
		return
	}
	if wantsText(r) {
		var b strings.Builder
		for _, v := range items {
			fmt.Fprintf(&b, "%d|%s|%s|%s\n", v.ID, v.Time.Format(time.RFC3339), oneLine(v.Name), oneLine(v.Body))
		}
		a.text(w, b.String())
		return
	}
	d := a.base("guestbook")
	d.Heading = "guestbook"
	d.Intro = fmt.Sprintf("Leave a note. This page is public. %d entr%s total.", total, plural(total))
	d.FormHTML = template.HTML(`<form method="post" action="/guestbook/"><p><label>name <input name="name" type="text" maxlength="80"></label></p><p><textarea name="body" maxlength="65536" placeholder="say something"></textarea></p><p><input type="submit" value="SIGN GUESTBOOK"></p></form>`)
	for _, v := range items {
		d.Items = append(d.Items, template.HTML(fmt.Sprintf(`<b><a href="/guestbook/%d">#%d</a> %s</b> <span class="small">%s</span><pre>%s</pre>`, v.ID, v.ID, template.HTMLEscapeString(v.Name), v.Time.Format("2006-01-02 15:04 UTC"), template.HTMLEscapeString(v.Body))))
	}
	d.PageNav = guestbookPageNav(page, perPage, total)
	a.render(w, a.tList, d)
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func guestbookPageNav(page, perPage, total int) template.HTML {
	if total <= perPage {
		return ""
	}
	pages := (total + perPage - 1) / perPage
	var b strings.Builder
	fmt.Fprintf(&b, `<nav class="pager" aria-label="guestbook pages"><span>page %d of %d</span>`, page, pages)
	if page > 1 {
		fmt.Fprintf(&b, ` <a href="/guestbook/?page=%d">newer</a>`, page-1)
	}
	if page < pages {
		fmt.Fprintf(&b, ` <a href="/guestbook/?page=%d">older</a>`, page+1)
	}
	b.WriteString(`</nav>`)
	return template.HTML(b.String())
}

func (a *app) guestbookItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	s := strings.TrimPrefix(r.URL.Path, "/guestbook/")
	ext := path.Ext(s)
	if ext == ".txt" || ext == ".json" {
		s = strings.TrimSuffix(s, ext)
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		a.notFound(w, r)
		return
	}
	v, ok := a.store.GuestbookByID(id)
	if !ok {
		a.notFound(w, r)
		return
	}
	if ext == ".json" || wantsJSON(r) {
		writeJSON(w, v)
		return
	}
	if ext == ".txt" || wantsText(r) {
		a.text(w, fmt.Sprintf("#%d\n%s\n%s\n\n%s\n", v.ID, v.Name, v.Time.Format(time.RFC3339), v.Body))
		return
	}
	d := a.base(fmt.Sprintf("guestbook #%d", id))
	d.Heading = fmt.Sprintf("guestbook #%d", id)
	d.BodyHTML = template.HTML(fmt.Sprintf(`<p><b>%s</b> <span class="small">%s</span></p><pre>%s</pre><p class="small"><a href="%d.txt">text</a> | <a href="%d.json">json</a></p>`, template.HTMLEscapeString(v.Name), v.Time.Format("2006-01-02 15:04 UTC"), template.HTMLEscapeString(v.Body), id, id))
	a.render(w, a.tPlain, d)
}

func (a *app) wall(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !a.allowWrite(w, r) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = "anon"
		}
		body := strings.TrimSpace(r.FormValue("body"))
		if body == "" {
			http.Error(w, "empty message", 400)
			return
		}
		if _, bad := safety.CredentialLike(body); bad {
			http.Error(w, "content resembles a credential or private key and was not stored", 422)
			return
		}
		v, err := a.store.AddWall(clip(name, 80), clip(body, int(a.maxWrite)))
		if err != nil {
			http.Error(w, "store error", 500)
			return
		}
		a.logWrite(r, "wall", strconv.FormatInt(v.ID, 10))
		if wantsJSON(r) {
			writeJSON(w, v)
			return
		}
		http.Redirect(w, r, "/wall/", 303)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, POST, OPTIONS")
		return
	}
	items := a.store.Wall()
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if after > 0 {
		var filtered []store.WallEntry
		for _, v := range items {
			if v.ID > after {
				filtered = append(filtered, v)
			}
		}
		items = filtered
	}
	if wantsJSON(r) {
		writeJSON(w, items)
		return
	}
	if wantsText(r) {
		var b strings.Builder
		for _, v := range items {
			fmt.Fprintf(&b, "%d|%d|%s|%s\n", v.ID, v.Time.Unix(), oneLine(v.Name), oneLine(v.Body))
		}
		a.text(w, b.String())
		return
	}
	d := a.base("wall")
	d.Heading = "the wall"
	d.Intro = "One long public scratch wall."
	d.FormHTML = template.HTML(`<form method="post" action="/wall/"><p><input name="name" type="text" maxlength="80" placeholder="name (optional)"></p><p><textarea name="body" maxlength="65536"></textarea></p><input type="submit" value="WRITE ON WALL"></form>`)
	for _, v := range items {
		d.Items = append(d.Items, template.HTML(fmt.Sprintf(`<span class="small">#%d %s</span> <b>%s</b><pre>%s</pre>`, v.ID, v.Time.Format("2006-01-02 15:04:05"), template.HTMLEscapeString(v.Name), template.HTMLEscapeString(v.Body))))
	}
	a.render(w, a.tList, d)
}

func (a *app) notesIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !a.allowWrite(w, r) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		p := r.FormValue("path")
		body := r.FormValue("body")
		if _, bad := safety.CredentialLike(body); bad {
			http.Error(w, "content resembles a credential or private key and was not stored", 422)
			return
		}
		v, err := a.store.PutNote(p, clip(body, int(a.maxWrite)), "text/plain")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.logWrite(r, "note", v.Path)
		http.Redirect(w, r, "/notes/"+url.PathEscape(v.Path), 303)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, POST, OPTIONS")
		return
	}
	notes := a.store.Notes(r.URL.Query().Get("prefix"))
	if wantsJSON(r) {
		writeJSON(w, notes)
		return
	}
	if wantsText(r) {
		var b strings.Builder
		for _, v := range notes {
			fmt.Fprintf(&b, "%s\t%d\t%s\n", v.Path, v.Version, v.Time.Format(time.RFC3339))
		}
		a.text(w, b.String())
		return
	}
	d := a.base("notes")
	d.Heading = "public notes"
	d.Intro = "A public hierarchical scratch namespace. Paths use letters, digits, '.', '-', '_', '~', '@', and '+'."
	d.FormHTML = template.HTML(`<form method="post" action="/notes/"><p><label>path /notes/<input name="path" type="text" placeholder="project/scratch"></label></p><p><textarea name="body"></textarea></p><input type="submit" value="SAVE NOTE"></form>`)
	for _, v := range notes {
		d.Items = append(d.Items, template.HTML(fmt.Sprintf(`<a class="path" href="/notes/%s">/notes/%s</a> <span class="small">v%d %s</span><pre>%s</pre>`, v.Path, template.HTMLEscapeString(v.Path), v.Version, v.Time.Format("2006-01-02 15:04"), template.HTMLEscapeString(clip(v.Body, 300)))))
	}
	a.render(w, a.tList, d)
}

func (a *app) note(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/notes/")
	if p == "" {
		a.notesIndex(w, r)
		return
	}
	if r.Method == http.MethodPut {
		if !a.allowWrite(w, r) {
			return
		}
		b, ok := a.readLimited(w, r)
		if !ok {
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		if !safety.SafeTextContentType(ct) {
			http.Error(w, "unsupported content type", 415)
			return
		}
		v, err := a.store.PutNote(p, string(b), ct)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.logWrite(r, "note", v.Path)
		writeJSON(w, v)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, PUT, OPTIONS")
		return
	}
	v, ok := a.store.Note(p)
	if !ok {
		a.notFound(w, r)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"note-%d"`, v.Version))
	w.Header().Set("Last-Modified", v.Time.UTC().Format(http.TimeFormat))
	w.Header().Add("Link", fmt.Sprintf(`</notes/%s?format=json>; rel="alternate"; type="application/json"`, v.Path))
	if wantsJSON(r) {
		writeJSON(w, v)
		return
	}
	if wantsText(r) || !accepts(r, "text/html") {
		w.Header().Set("Content-Type", v.ContentType+"; charset=utf-8")
		_, _ = io.WriteString(w, v.Body)
		return
	}
	d := a.base(v.Path)
	d.Heading = "/notes/" + v.Path
	d.BodyHTML = template.HTML(fmt.Sprintf(`<pre>%s</pre><p class="small">version %d · %s · <a href="?format=txt">text</a> · <a href="?format=json">json</a></p>`, template.HTMLEscapeString(v.Body), v.Version, v.Time.Format(time.RFC3339)))
	a.render(w, a.tPlain, d)
}

func (a *app) files(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !a.allowWrite(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, a.maxWrite+64*1024)
		if err := r.ParseMultipartForm(a.maxWrite); err != nil {
			http.Error(w, "bad upload", 400)
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file field required", 400)
			return
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, a.maxWrite+1))
		if err != nil || int64(len(b)) > a.maxWrite {
			http.Error(w, "file too large", 413)
			return
		}
		ct := h.Header.Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(b)
		}
		if !safety.SafeTextContentType(ct) {
			http.Error(w, "text/json/csv/xml/svg only", 415)
			return
		}
		if kind, bad := safety.CredentialLike(string(b)); bad {
			http.Error(w, "file resembles a credential or private key ("+kind+") and was not stored", 422)
			return
		}
		v, err := a.store.AddFile(h.Filename, ct, b)
		if err != nil {
			http.Error(w, "store error", 500)
			return
		}
		a.logWrite(r, "file", strconv.FormatInt(v.ID, 10))
		http.Redirect(w, r, fmt.Sprintf("/files/%d/%s", v.ID, url.PathEscape(v.Name)), 303)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, POST, OPTIONS")
		return
	}
	items := a.store.Files()
	if wantsJSON(r) {
		writeJSON(w, items)
		return
	}
	d := a.base("files")
	d.Heading = "file cabinet"
	d.Intro = "Small public text-ish files only (64 KiB by default)."
	d.FormHTML = template.HTML(`<form method="post" action="/files/" enctype="multipart/form-data"><input type="file" name="file" accept=".txt,.md,.json,.csv,.xml,.svg,text/plain,text/markdown,application/json"><input type="submit" value="UPLOAD"></form>`)
	for _, v := range items {
		d.Items = append(d.Items, template.HTML(fmt.Sprintf(`<a href="/files/%d/%s">%s</a> <span class="small">%d bytes · %s · sha256:%s</span>`, v.ID, url.PathEscape(v.Name), template.HTMLEscapeString(v.Name), v.Size, v.Time.Format("2006-01-02"), v.SHA256[:12])))
	}
	a.render(w, a.tList, d)
}

func (a *app) fileItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/files/"), "/")
	if len(parts) < 1 {
		a.notFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		a.notFound(w, r)
		return
	}
	v, p, ok := a.store.File(id)
	if !ok {
		a.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", v.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, v.Name))
	http.ServeFile(w, r, p)
}

func (a *app) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	q := r.URL.Query().Get("q")
	results := a.store.Search(q)
	if wantsJSON(r) {
		writeJSON(w, results)
		return
	}
	d := a.base("search")
	d.Heading = "search"
	d.FormHTML = template.HTML(`<form method="get" action="/search"><input type="search" name="q" value="` + template.HTMLEscapeString(q) + `"><input type="submit" value="SEARCH"></form>`)
	for _, v := range results {
		d.Items = append(d.Items, template.HTML(fmt.Sprintf(`<b>%s</b> <a href="%s">%s</a><br><span class="small">%s</span><pre>%s</pre>`, template.HTMLEscapeString(v.Kind), template.HTMLEscapeString(v.URL), template.HTMLEscapeString(v.Title), v.Time.Format(time.RFC3339), template.HTMLEscapeString(v.Snippet))))
	}
	a.render(w, a.tList, d)
}

func (a *app) feedItems() []map[string]any {
	var out []map[string]any
	for _, v := range a.store.Guestbook() {
		out = append(out, map[string]any{"kind": "guestbook", "id": v.ID, "time": v.Time, "name": v.Name, "body": v.Body, "url": fmt.Sprintf("/guestbook/%d", v.ID)})
	}
	for _, v := range a.store.Wall() {
		out = append(out, map[string]any{"kind": "wall", "id": v.ID, "time": v.Time, "name": v.Name, "body": v.Body, "url": "/wall/"})
	}
	for _, v := range a.store.Notes("") {
		out = append(out, map[string]any{"kind": "note", "id": v.Path, "time": v.Time, "name": v.Path, "body": v.Body, "url": "/notes/" + v.Path})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, _ := out[i]["time"].(time.Time)
		tj, _ := out[j]["time"].(time.Time)
		return ti.After(tj)
	})
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
func (a *app) feedJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	writeJSON(w, a.feedItems())
}
func (a *app) feedNDJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	enc := json.NewEncoder(w)
	for _, v := range a.feedItems() {
		_ = enc.Encode(v)
	}
}

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Xmlns   string      `xml:"xmlns,attr"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Entries []atomEntry `xml:"entry"`
}
type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Content string   `xml:"content"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
}

func (a *app) feedAtom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	f := atomFeed{Xmlns: "http://www.w3.org/2005/Atom", Title: a.siteName, ID: a.baseURL + "/", Updated: time.Now().UTC().Format(time.RFC3339)}
	for _, v := range a.feedItems() {
		t, _ := v["time"].(time.Time)
		u := fmt.Sprint(v["url"])
		f.Entries = append(f.Entries, atomEntry{Title: fmt.Sprintf("%s: %s", v["kind"], v["name"]), ID: a.baseURL + u, Updated: t.Format(time.RFC3339), Link: atomLink{Href: a.baseURL + u}, Content: fmt.Sprint(v["body"])})
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(f)
}

func (a *app) handleOptions(w http.ResponseWriter, r *http.Request) {
	allow := "GET, HEAD, OPTIONS"
	switch {
	case r.URL.Path == "/guestbook/" || r.URL.Path == "/wall/" || r.URL.Path == "/notes/" || r.URL.Path == "/files/":
		allow = "GET, HEAD, POST, OPTIONS"
	case strings.HasPrefix(r.URL.Path, "/notes/"):
		allow = "GET, HEAD, PUT, OPTIONS"
	}
	w.Header().Set("Allow", allow)
	w.Header().Set("Accept-Post", "application/x-www-form-urlencoded, application/json, multipart/form-data")
	w.Header().Set("Accept-Put", "text/plain, text/markdown, application/json, text/csv, application/xml, image/svg+xml")
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) robots(w http.ResponseWriter, r *http.Request) {
	a.text(w, "User-agent: *\nAllow: /\nSitemap: "+a.baseURL+"/sitemap.xml\n")
}
func (a *app) sitemap(w http.ResponseWriter, r *http.Request) {
	type U struct {
		Loc string `xml:"loc"`
	}
	type US struct {
		XMLName xml.Name `xml:"urlset"`
		Xmlns   string   `xml:"xmlns,attr"`
		URLs    []U      `xml:"url"`
	}
	s := US{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, p := range []string{"/", "/guestbook/", "/wall/", "/notes/", "/files/", "/links.html", "/help.txt", "/feed.atom", "/reference/", "/reference/plain-text.txt", "/reference/text-formats.txt", "/reference/backup-checklist.txt", "/reference/changes.txt", "/reference/http-relay.txt"} {
		s.URLs = append(s.URLs, U{a.baseURL + p})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(s)
}
func (a *app) manifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"name": a.siteName, "short_name": "relay", "start_url": "/", "display": "minimal-ui"})
}

func (a *app) openSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/opensearchdescription+xml; charset=utf-8")
	q := a.baseURL + "/search?q={searchTerms}"
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/"><ShortName>Relay Search</ShortName><Description>Search this site</Description><Url type="text/html" template="%s"/><Url type="application/json" template="%s&amp;format=json"/></OpenSearchDescription>`, q, q)
}

func (a *app) hostMetaJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"links": []map[string]string{
		{"rel": "help", "href": a.baseURL + "/help.txt"},
		{"rel": "alternate", "type": "application/atom+xml", "href": a.baseURL + "/feed.atom"},
		{"rel": "collection", "href": a.baseURL + "/notes/"},
	}})
}

func (a *app) hostMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xrd+xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Link rel="help" href="%s/help.txt"/><Link rel="alternate" type="application/atom+xml" href="%s/feed.atom"/><Link rel="collection" href="%s/notes/"/></XRD>`, a.baseURL, a.baseURL, a.baseURL)
}
func (a *app) siteMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"name": a.siteName, "home": a.baseURL + "/", "help": a.baseURL + "/help.txt", "collections": []string{a.baseURL + "/guestbook/", a.baseURL + "/wall/", a.baseURL + "/notes/", a.baseURL + "/files/"}, "feeds": []string{a.baseURL + "/feed.json", a.baseURL + "/feed.ndjson", a.baseURL + "/feed.atom"}})
}
func (a *app) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		http.Error(w, "resource required", 400)
		return
	}
	writeJSON(w, map[string]any{"subject": resource, "links": []map[string]string{{"rel": "self", "href": a.baseURL + "/"}, {"rel": "http://webfinger.net/rel/profile-page", "href": a.baseURL + "/guestbook/"}}})
}
func (a *app) nodeInfoDiscovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"links": []map[string]string{{"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": a.baseURL + "/nodeinfo/2.1"}}})
}
func (a *app) nodeInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"version": "2.1", "software": map[string]string{"name": "relaybox", "version": "0.1"}, "protocols": []string{}, "services": map[string][]string{"inbound": {}, "outbound": {}}, "openRegistrations": true, "usage": map[string]any{"users": map[string]int{"total": 0}, "localPosts": len(a.feedItems())}, "metadata": map[string]any{"guestbook": true, "publicNotes": true}})
}

func (a *app) readmeText() string {
	return "WELCOME\n\nThis is a small personal web server.\n\nMost pages can be read normally in a browser.\n\nSome resources also have plain-text or structured versions.\n\nUseful places:\n\n/guestbook/\n/wall/\n/notes/\n/files/\n/archive/\n\nEverything in these directories should be assumed public.\n\nStable links are intentional.\n\nIf you find something useful, you may link directly to it.\n"
}
func (a *app) helpText() string {
	return `SITE HELP
=========

Everything here is public.

GUESTBOOK
  GET  /guestbook/
  POST /guestbook/            form fields: name, body, reply_to
  GET  /guestbook/<id>.txt
  GET  /guestbook/<id>.json

WALL
  GET  /wall/
  GET  /wall.txt?after=<id>
  GET  /wall.json?after=<id>
  POST /wall/                 form fields: name, body

NOTES
  GET  /notes/
  GET  /notes/<path>
  PUT  /notes/<path>          request body becomes current note
  GET  /notes/<path>?format=json
  POST /notes/                form fields: path, body

FILES
  GET  /files/
  POST /files/                multipart field: file

FEEDS
  /feed.json
  /feed.ndjson
  /feed.atom

DISCOVERY
  /robots.txt
  /sitemap.xml
  /.well-known/host-meta
  /.well-known/host-meta.json
  /.well-known/site-meta.json
  /opensearch.xml

Many collection endpoints honor Accept: text/plain or application/json.
OPTIONS returns supported methods.

Maximum write size is intentionally small. Text-like uploads only.
Credentials and private keys are rejected when recognized.
`
}

func (a *app) observatory(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		http.Error(w, "not found", 404)
		return
	}
	events, err := a.store.EventTail(250)
	if err != nil {
		http.Error(w, "event read error", 500)
		return
	}
	var b strings.Builder
	b.WriteString(`<p>Private request/event tail. Remote addresses are HMAC-pseudonymized before storage.</p><p><a href="/__events.ndjson">raw NDJSON</a></p><pre>`)
	for i := len(events) - 1; i >= 0; i-- {
		v := events[i]
		fmt.Fprintf(&b, "%s %3d %-5s %-16s %-20s %s", v.Time.Format("15:04:05"), v.Status, v.Method, v.RemoteHash, template.HTMLEscapeString(clip(v.UserAgent, 20)), template.HTMLEscapeString(v.Path))
		if v.WriteType != "" {
			fmt.Fprintf(&b, "  WRITE %s %s", v.WriteType, v.ObjectID)
		}
		b.WriteByte('\n')
	}
	b.WriteString(`</pre>`)
	d := a.base("observatory")
	d.Heading = "observatory"
	d.BodyHTML = template.HTML(b.String())
	a.render(w, a.tPlain, d)
}
func (a *app) events(w http.ResponseWriter, r *http.Request) {
	if !a.admin(r) {
		http.Error(w, "not found", 404)
		return
	}
	events, err := a.store.EventTail(1000)
	if err != nil {
		http.Error(w, "event read error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, v := range events {
		_ = enc.Encode(v)
	}
}
func (a *app) admin(r *http.Request) bool {
	if a.adminToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	return hmac.Equal([]byte(auth), []byte("Bearer "+a.adminToken))
}

func (a *app) notFound(w http.ResponseWriter, r *http.Request) {
	if accepts(r, "application/json") {
		w.WriteHeader(404)
		writeJSON(w, map[string]string{"error": "not found"})
		return
	}
	d := a.base("404")
	d.Heading = "404 file not found"
	d.BodyHTML = template.HTML(`<p>The requested file wandered off.</p><p><a href="/">home</a> · <a href="/search">search</a> · <a href="/help.txt">help.txt</a></p>`)
	w.WriteHeader(404)
	a.render(w, a.tPlain, d)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// Keep these imports exercised even if a future build strips optional aliases.
var _ = bytes.MinRead
var _ = mime.TypeByExtension
