package api

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/core"
)

// The folder picker of the web interface: the page cannot look at the disk itself, so it asks
// here. Only holders of the access key get this, like everything else in the API.

const maxFsEntries = 3000

type fsEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type fsPlace struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"` // data | home | desktop | documents | downloads | music | pictures | videos | drive | place
	diskSpace
}

// diskSpace is the size of the disk a folder is on and what is free there, for the bar under a place; both are
// left out when the system does not say (a network folder, a drive that did not answer in time).
type diskSpace struct {
	TotalBytes *uint64 `json:"totalBytes,omitempty"`
	FreeBytes  *uint64 `json:"freeBytes,omitempty"`
}

type fsListing struct {
	Path      string    `json:"path"`   // "" while the list of drives is shown
	Parent    *string   `json:"parent"` // null when there is no level above
	Crumbs    []fsPlace `json:"crumbs"` // the way from the top to Path
	Entries   []fsEntry `json:"entries"`
	Places    []fsPlace `json:"places"`              // quick links for the side list
	Error     string    `json:"error,omitempty"`     // why the folder could not be read
	ErrorCode string    `json:"errorCode,omitempty"` // the same as a code (see core.Coder), when there is one
	Truncated bool      `json:"truncated,omitempty"`
	diskSpace           // of the disk Path is on: "Свободно 360 ГБ" under the chosen folder
}

// Place is a folder the server is given by the system around it, under the name that system shows it by (see
// Server.SetPlaces).
type Place struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// placeOf returns the place p lies in (the deepest one, if places nest).
func (s *Server) placeOf(p string) (Place, bool) {
	var best Place
	found := false
	for _, pl := range s.places {
		root := filepath.Clean(pl.Path)
		if (p == root || strings.HasPrefix(p, root+string(filepath.Separator))) && (!found || len(root) > len(best.Path)) {
			best, found = Place{Name: pl.Name, Path: root}, true
		}
	}
	return best, found
}

// rootsMark is the path a client sends to see the drives.
const rootsMark = "@"

func (s *Server) fsList(w http.ResponseWriter, r *http.Request) {
	out := s.listing(strings.TrimSpace(r.URL.Query().Get("path")))
	out.measure()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listing(want string) fsListing {
	out := fsListing{Places: s.fsPlaces(), Entries: []fsEntry{}, Crumbs: []fsPlace{}}
	if want == rootsMark && runtime.GOOS == "windows" {
		out.showDrives()
		return out
	}
	if want == "" || want == rootsMark {
		want = s.cfg.Get().DataDir
	}
	if len(s.places) > 0 {
		s.placeListing(&out, filepath.Clean(want))
		return out
	}
	p, ok := nearestFolder(filepath.Clean(want))
	if !ok {
		if runtime.GOOS == "windows" {
			out.showDrives()
		}
		return out
	}
	out.Path = p
	out.readInto(p)
	return out
}

// diskUsage is core.DiskUsage; tests replace it.
var diskUsage = core.DiskUsage

// spaceWait is how long a listing waits for the disks to say how full they are: a sleeping external drive or a
// network one that does not answer goes without its bar rather than holding up the window.
var spaceWait = 2 * time.Second

// measure fills in the disk space of the drives and given places in the side list (not of the usual folders, which
// are all on the system drive) and of the folder shown, all at once and once per listing: the window asks again
// when it opens and when the folder changes, never on its own.
func (l *fsListing) measure() {
	type job struct {
		path string
		to   *diskSpace
	}
	var jobs []job
	for i := range l.Places {
		if k := l.Places[i].Kind; k == "drive" || k == "place" || k == "data" {
			jobs = append(jobs, job{l.Places[i].Path, &l.Places[i].diskSpace})
		}
	}
	if l.Path != "" {
		jobs = append(jobs, job{l.Path, &l.diskSpace})
	}
	type result struct {
		i           int
		total, free uint64
	}
	done := make(chan result, len(jobs))
	// read here, not in the goroutines: one that is late (or never answers) must not touch the variable after the listing
	// is over, when a test puts the real function back
	usage := diskUsage
	for i, j := range jobs {
		go func() {
			total, free, err := usage(j.path)
			if err != nil || total == 0 { // a file system that does not know its size: no bar
				total = 0
			}
			done <- result{i, total, free}
		}()
	}
	timeout := time.After(spaceWait)
	for range jobs {
		select {
		case r := <-done:
			if r.total > 0 {
				total, free := r.total, min(r.free, r.total)
				*jobs[r.i].to = diskSpace{TotalBytes: &total, FreeBytes: &free}
			}
		case <-timeout:
			return
		}
	}
}

// nearestFolder walks up from p until it finds a folder that exists (a typed path may not).
func nearestFolder(p string) (string, bool) {
	for {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p, true
		}
		up := filepath.Dir(p)
		if up == p {
			return "", false
		}
		p = up
	}
}

// placeListing shows a folder inside the given places only: a path outside them (or one that no longer exists)
// shows the first place, "up" stops at the top of a place, and the way there starts with the place's name.
func (s *Server) placeListing(out *fsListing, want string) {
	p, ok := nearestFolder(want)
	pl, in := s.placeOf(p)
	if !ok || !in {
		if len(out.Places) == 0 {
			out.Error, out.ErrorCode = "Папка не найдена.", "fs.not_found"
			return
		}
		p = out.Places[0].Path
		pl, _ = s.placeOf(p)
	}
	out.Path = p
	out.readInto(p)
	out.Parent = nil
	if p != pl.Path {
		up := filepath.Dir(p)
		out.Parent = &up
	}
	out.Crumbs = []fsPlace{{Name: pl.Name, Path: pl.Path}}
	cur := pl.Path
	for _, part := range strings.Split(strings.Trim(strings.TrimPrefix(p, pl.Path), `\/`), string(filepath.Separator)) {
		if part != "" {
			cur = filepath.Join(cur, part)
			out.Crumbs = append(out.Crumbs, fsPlace{Name: part, Path: cur})
		}
	}
}

func (l *fsListing) showDrives() {
	l.Path, l.Parent = "", nil
	l.Crumbs = []fsPlace{{Name: computerName, Path: rootsMark}}
	for _, d := range drives() {
		l.Entries = append(l.Entries, fsEntry{Name: d.Name, Path: d.Path, Dir: true})
	}
}

func (l *fsListing) readInto(p string) {
	// where "up" leads, and the way there
	if up := filepath.Dir(p); up != p {
		l.Parent = &up
	} else if runtime.GOOS == "windows" {
		m := rootsMark
		l.Parent = &m
	}
	l.Crumbs = crumbsOf(p)

	des, err := os.ReadDir(p)
	if err != nil {
		l.Error, l.ErrorCode = friendlyFsError(err)
		return
	}
	for _, de := range des {
		full := filepath.Join(p, de.Name())
		if hiddenEntry(full, de.Name()) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		isDir := de.IsDir()
		if de.Type()&os.ModeSymlink != 0 { // a link to a folder counts as a folder
			if st, err := os.Stat(full); err == nil {
				isDir, info = st.IsDir(), st
			}
		}
		e := fsEntry{Name: de.Name(), Path: full, Dir: isDir, Modified: info.ModTime()}
		if !isDir {
			e.Size = info.Size()
		}
		l.Entries = append(l.Entries, e)
		if len(l.Entries) >= maxFsEntries {
			l.Truncated = true
			break
		}
	}
	sort.SliceStable(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// friendlyFsError words a failure to read a folder, with its code ("" when it is only the text of the system).
func friendlyFsError(err error) (msg, code string) {
	switch {
	case os.IsPermission(err):
		return "Нет доступа к этой папке.", "fs.no_access"
	case os.IsNotExist(err):
		return "Папка не найдена.", "fs.not_found"
	}
	return err.Error(), ""
}

// crumbsOf splits a path into clickable steps: "C:\a\b" gives the computer, "C:\", "a", "b".
func crumbsOf(p string) []fsPlace {
	var out []fsPlace
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p, vol)
	root := vol + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		out = append(out, fsPlace{Name: computerName, Path: rootsMark})
	}
	rootName := root
	if vol != "" {
		rootName = vol
	}
	out = append(out, fsPlace{Name: rootName, Path: root})
	cur := root
	for _, part := range strings.Split(strings.Trim(rest, `\/`), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		out = append(out, fsPlace{Name: part, Path: cur})
	}
	return out
}

// fsPlaces lists the quick links: the download folder, the user's usual folders and the drives.
func (s *Server) fsPlaces() []fsPlace {
	var out []fsPlace
	add := func(name, path, kind string) {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			out = append(out, fsPlace{Name: name, Path: path, Kind: kind})
		}
	}
	if len(s.places) > 0 { // the given folders only, the ones that are there (a slot left empty is not)
		for _, pl := range s.places {
			add(pl.Name, filepath.Clean(pl.Path), "place")
		}
		return out
	}
	add("Папка загрузок", s.cfg.Get().DataDir, "data")
	if home, err := os.UserHomeDir(); err == nil {
		add("Домашняя папка", home, "home")
		add("Рабочий стол", filepath.Join(home, "Desktop"), "desktop")
		add("Документы", filepath.Join(home, "Documents"), "documents")
		add("Загрузки", filepath.Join(home, "Downloads"), "downloads")
		add("Музыка", filepath.Join(home, "Music"), "music")
		add("Изображения", filepath.Join(home, "Pictures"), "pictures")
		add("Видео", filepath.Join(home, "Videos"), "videos")
	}
	out = append(out, drives()...)
	if runtime.GOOS != "windows" {
		out = append(out, fsPlace{Name: "/", Path: "/", Kind: "drive"})
	}
	return out
}

// fsMkdir creates a folder inside another and returns its path.
func (s *Server) fsMkdir(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decode(r, &b); err != nil {
		fail(w, core.ErrInvalidInput)
		return
	}
	name := strings.TrimSpace(b.Name)
	switch {
	case name == "" || name == "." || name == "..":
		fail(w, badInputCode("fs.name_empty", "Введите имя папки."))
		return
	case strings.ContainsAny(name, `<>:"/\|?*`) || strings.ContainsRune(name, 0):
		fail(w, badInputCode("fs.name_chars", `Имя не должно содержать символы < > : " / \ | ? *`))
		return
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		fail(w, badInputCode("fs.name_end", "Имя не должно заканчиваться точкой или пробелом."))
		return
	}
	parent := filepath.Clean(strings.TrimSpace(b.Parent))
	if _, in := s.placeOf(parent); len(s.places) > 0 && !in {
		fail(w, badInputCode("fs.parent_missing", "Папка, в которой нужно создать новую, не найдена."))
		return
	}
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		fail(w, badInputCode("fs.parent_missing", "Папка, в которой нужно создать новую, не найдена."))
		return
	}
	full := filepath.Join(parent, name)
	if err := os.Mkdir(full, 0o755); err != nil {
		if os.IsExist(err) {
			fail(w, badInputCode("fs.exists", "Такая папка уже есть."))
			return
		}
		msg, code := friendlyFsError(err)
		fail(w, badInputCode(code, msg))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": full})
}

// stagePath puts a .torrent that lies on the server (on Umbrel: in Downloads or a folder added in the app's settings)
// on the "Add torrents" list, like a file uploaded from the browser: {"path": "/downloads/x.torrent"}. With places
// (see SetPlaces) the file must be inside one of them, as for a new folder.
//
// Several files chosen at once come as {"paths": [...]}: each is staged on its own, one that fails does not stop the
// others, and the answer (200) lists both: {"staged": [...], "failed": [{"path", "error", "code", "args"}]}.
func (s *Server) stagePath(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := decode(r, &b); err != nil || (b.Paths != nil && len(b.Paths) == 0) || len(b.Paths) > maxStagePaths {
		fail(w, core.ErrInvalidInput)
		return
	}
	if b.Paths == nil {
		staged, err := s.stageOne(b.Path)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, staged)
		return
	}
	type failure struct {
		Path string `json:"path"`
		errorBody
	}
	out := struct {
		Staged []core.StagedTorrent `json:"staged"`
		Failed []failure            `json:"failed"`
	}{Staged: []core.StagedTorrent{}, Failed: []failure{}}
	for _, p := range b.Paths {
		staged, err := s.stageOne(p)
		if err != nil {
			out.Failed = append(out.Failed, failure{Path: p, errorBody: bodyOf(err)})
			continue
		}
		out.Staged = append(out.Staged, staged)
	}
	writeJSON(w, http.StatusOK, out)
}

// maxStagePaths is how many files one request may stage: a folder of .torrent files chosen whole is fine, a list
// without end is not.
const maxStagePaths = 1000

// stageOne stages one .torrent on the server (see stagePath).
func (s *Server) stageOne(path string) (core.StagedTorrent, error) {
	p := filepath.Clean(strings.TrimSpace(path))
	name := filepath.Base(p)
	missing := badInputCode("stage.no_file", "Файл не найден: «"+name+"».", name)
	if _, in := s.placeOf(p); !filepath.IsAbs(p) || (len(s.places) > 0 && !in) {
		return core.StagedTorrent{}, missing
	}
	if !strings.EqualFold(filepath.Ext(p), ".torrent") {
		return core.StagedTorrent{}, badInputCode("stage.not_torrent", "Это не файл .torrent: «"+name+"».", name)
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return core.StagedTorrent{}, missing
	}
	if st.Size() > maxTorrentFile {
		mib := fmt.Sprint(maxTorrentFile >> 20)
		return core.StagedTorrent{}, badInputCode("stage.too_large", "Файл больше "+mib+" МиБ: это не .torrent.", mib)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		msg, code := friendlyFsError(err)
		return core.StagedTorrent{}, badInputCode(code, msg)
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		return core.StagedTorrent{}, badInputCode("stage.not_torrent", "Это не файл .torrent: «"+name+"».", name)
	}
	return s.m.Stage(mi, raw)
}

// inputError is an ErrInvalidInput with a message meant for the person using the page.
type inputError struct {
	msg  string
	code string
	args []string
}

func badInput(msg string) error { return &inputError{msg: msg} }

// badInputCode is badInput with a code and the pieces of the text that change (see core.Coder); an empty code is none.
func badInputCode(code, msg string, args ...string) error {
	return &inputError{msg: msg, code: code, args: args}
}
func (e *inputError) Error() string   { return e.msg }
func (e *inputError) Is(t error) bool { return t == core.ErrInvalidInput }

// ErrCode makes the error a core.Coder; without a code it is not one (the client gets the text only).
func (e *inputError) ErrCode() (string, []string) { return e.code, e.args }
