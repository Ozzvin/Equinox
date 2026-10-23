package core

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent"
)

// Kinds of failure that stop a torrent.
const (
	ErrKindDiskFull = "disk_full"
	ErrKindWrite    = "write"
	ErrKindMissing  = "missing" // files that were on the disk are not there any more (see missing.go)
	ErrKindOther    = "other"
)

type torrentError struct {
	kind, msg string
	code      string   // for a client in another language (see Coder), "" if the message has none
	args      []string // the code's arguments
	// ErrKindMissing only: the files not found, whether the torrent was paused already when they went missing, and
	// whether a check of the files that came back is under way
	files   []int
	paused  bool
	recheck bool
}

// diskFree reports the free space of the drive holding dir. It is a variable so tests can
// pretend that the disk is full.
var diskFree = freeSpace

func (m *Manager) setError(hash, kind, msg string) {
	m.mu.Lock()
	m.errs[hash] = torrentError{kind: kind, msg: msg}
	m.mu.Unlock()
}

func (m *Manager) clearError(hash string) {
	m.mu.Lock()
	delete(m.errs, hash)
	m.mu.Unlock()
}

// clearSetupError forgets a failure of the disk preparation (no room, an allocation that failed), but keeps a
// write error: begin runs in the background after the torrent is added, and the engine can report a failing write
// while it is still preparing the disk; a finished preparation must not wipe that report.
func (m *Manager) clearSetupError(hash string) {
	m.mu.Lock()
	if e, ok := m.errs[hash]; ok && e.kind != ErrKindWrite {
		delete(m.errs, hash)
	}
	m.mu.Unlock()
}

// watchWrites makes a failing write to disk visible: the engine stops the download on its
// own but says nothing, so the user would just see a torrent that does not move.
func (m *Manager) watchWrites(t *torrent.Torrent, hash string) {
	t.SetOnWriteChunkError(func(err error) { m.onWriteError(t, hash, err) })
}

func (m *Manager) onWriteError(t *torrent.Torrent, hash string, err error) {
	m.mu.Lock()
	_, already := m.errs[hash]
	m.mu.Unlock()
	if already {
		return // one report per failure; the engine may call this for every chunk
	}
	fmt.Fprintf(os.Stderr, "write error in %s: %v\n", t.Name(), err)
	m.setError(hash, ErrKindWrite, err.Error())
	m.setPaused(t, hash, true)
}

// Coder is an error that names itself with a code and gives the pieces of its text that change (a file name, an
// address). A client that speaks another language than the message builds the text from them: the code says which
// text, the arguments fill in the blanks. The message stays what the person reads in Russian, and the log.
type Coder interface {
	ErrCode() (code string, args []string)
}

// CodedError is a plain Coder.
type CodedError struct {
	Code string
	Args []string
	Msg  string
	Err  error // what caused it, if anything
}

func (e *CodedError) Error() string               { return e.Msg }
func (e *CodedError) Unwrap() error               { return e.Err }
func (e *CodedError) ErrCode() (string, []string) { return e.Code, e.Args }

// errInvalid builds an ErrInvalidInput error with a message.
func errInvalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, a...)...)
}

// Codes of "not enough disk space" (ErrNoDiskSpace). Their arguments are byte counts, so the page writes the sizes in
// its own units and language.
const (
	CodeNoSpace       = "disk.no_space"        // need, free
	CodeNoSpaceOthers = "disk.no_space_others" // need, others, free: the other active torrents on the disk still need others
	CodeNoSpaceMove   = "move.no_space"        // need, free in the target folder
)

// noSpace is ErrNoDiskSpace with its numbers: "На диске не хватает 2,4 ГБ (нужно 3,2 ГБ, свободно 0,8 ГБ)".
func noSpace(code string, need, others, free uint64) error {
	total := need + others
	short := total - min(free, total)
	args := []string{strconv.FormatUint(need, 10), strconv.FormatUint(free, 10)}
	msg := fmt.Sprintf("На диске не хватает %s (нужно %s, свободно %s)", sizeRu(short), sizeRu(total), sizeRu(free))
	switch code {
	case CodeNoSpaceOthers:
		args = []string{args[0], strconv.FormatUint(others, 10), args[1]}
		msg += fmt.Sprintf(", считая %s для других активных раздач на этом диске", sizeRu(others))
	case CodeNoSpaceMove:
		msg = fmt.Sprintf("На диске не хватает %s (нужно %s, свободно в целевой папке %s)", sizeRu(short), sizeRu(total), sizeRu(free))
	}
	return &CodedError{Code: code, Args: args, Msg: msg, Err: ErrNoDiskSpace}
}

// sizeRu writes a size as the list of torrents does (app.js bytes()), with the Russian decimal comma.
func sizeRu(n uint64) string {
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 100 || i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", v, units[i]), ".", ",", 1)
}

// setErrorOf is setError for an error that may carry a code (see Coder), which goes to the page with it.
func (m *Manager) setErrorOf(hash, kind string, err error) {
	e := torrentError{kind: kind, msg: err.Error()}
	var c Coder
	if errors.As(err, &c) {
		e.code, e.args = c.ErrCode()
	}
	m.mu.Lock()
	m.errs[hash] = e
	m.mu.Unlock()
}
