package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

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
	Kind string `json:"kind"` // data | home | desktop | documents | downloads | music | pictures | videos | drive
}

type fsListing struct {
	Path      string    `json:"path"`   // "" while the list of drives is shown
	Parent    *string   `json:"parent"` // null when there is no level above
	Crumbs    []fsPlace `json:"crumbs"` // the way from the top to Path
	Entries   []fsEntry `json:"entries"`
	Places    []fsPlace `json:"places"`          // quick links for the side list
	Error     string    `json:"error,omitempty"` // why the folder could not be read
	Truncated bool      `json:"truncated,omitempty"`
}

// rootsMark is the path a client sends to see the drives.
const rootsMark = "@"

func (s *Server) fsList(w http.ResponseWriter, r *http.Request) {
	want := strings.TrimSpace(r.URL.Query().Get("path"))
	out := fsListing{Places: s.fsPlaces(), Entries: []fsEntry{}, Crumbs: []fsPlace{}}

	if want == rootsMark && runtime.GOOS == "windows" {
		out.showDrives()
		writeJSON(w, http.StatusOK, out)
		return
	}
	if want == "" || want == rootsMark {
		want = s.cfg.Get().DataDir
	}
	p, ok := nearestFolder(filepath.Clean(want))
	if !ok {
		if runtime.GOOS == "windows" {
			out.showDrives()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Path = p
	out.readInto(p)
	writeJSON(w, http.StatusOK, out)
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
		l.Error = friendlyFsError(err)
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

func friendlyFsError(err error) string {
	switch {
	case os.IsPermission(err):
		return "Нет доступа к этой папке."
	case os.IsNotExist(err):
		return "Папка не найдена."
	}
	return err.Error()
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
		fail(w, badInput("Введите имя папки."))
		return
	case strings.ContainsAny(name, `<>:"/\|?*`) || strings.ContainsRune(name, 0):
		fail(w, badInput(`Имя не должно содержать символы < > : " / \ | ? *`))
		return
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		fail(w, badInput("Имя не должно заканчиваться точкой или пробелом."))
		return
	}
	parent := filepath.Clean(strings.TrimSpace(b.Parent))
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		fail(w, badInput("Папка, в которой нужно создать новую, не найдена."))
		return
	}
	full := filepath.Join(parent, name)
	if err := os.Mkdir(full, 0o755); err != nil {
		if os.IsExist(err) {
			fail(w, badInput("Такая папка уже есть."))
			return
		}
		fail(w, badInput(friendlyFsError(err)))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": full})
}

// inputError is an ErrInvalidInput with a message meant for the person using the page.
type inputError struct{ msg string }

func badInput(msg string) error       { return &inputError{msg} }
func (e *inputError) Error() string   { return e.msg }
func (e *inputError) Is(t error) bool { return t == core.ErrInvalidInput }
