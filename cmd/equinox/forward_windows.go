package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/Ozzvin/equinox/internal/app"
	"github.com/Ozzvin/equinox/internal/core"
)

// isMagnet reports whether a command-line argument is a magnet link.
func isMagnet(s string) bool { return strings.HasPrefix(strings.ToLower(s), "magnet:") }

// addArgs queues magnet links and .torrent files given on the command line (this is how Windows
// hands them over when the app is the registered handler) for the "Add torrents" dialog, instead
// of adding them outright: the user still sees which files are chosen and can set the save path
// before anything is downloaded.
func addArgs(a *app.App, args []string) {
	for _, arg := range args {
		switch {
		case isMagnet(arg):
			a.Manager.QueueExternalAdd(core.PendingAdd{Kind: "magnet", Magnet: arg})
		case strings.EqualFold(filepath.Ext(arg), ".torrent"):
			mi, err := metainfo.LoadFromFile(arg)
			if err != nil {
				logf("open %q: %v", arg, err)
				continue
			}
			st, err := a.Manager.Stage(mi)
			if err != nil {
				logf("stage %q: %v", arg, err)
				continue
			}
			a.Manager.QueueExternalAdd(core.PendingAdd{Kind: "stage", Stage: st.ID})
		}
	}
}

// forward hands command-line arguments to the already running instance through its local API, the
// same way addArgs does within this process: they end up in the "Add torrents" dialog rather than
// being added at once. The instance's address and access key are in ui-url next to the settings.
func forward(stateDir string, args []string) error {
	raw, err := os.ReadFile(filepath.Join(stateDir, "ui-url"))
	if err != nil {
		return err
	}
	base, token, ok := strings.Cut(strings.TrimSpace(string(raw)), "/#token=")
	if !ok {
		return errors.New("ui-url is malformed")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(path string, body io.Reader, contentType string) ([]byte, error) {
		req, err := http.NewRequest("POST", base+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", contentType)
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if res.StatusCode >= 300 {
			return nil, fmt.Errorf("%s: %s", res.Status, b)
		}
		return b, nil
	}
	queue := func(body map[string]string) error {
		b, _ := json.Marshal(body)
		_, err := do("/api/pending-add", bytes.NewReader(b), "application/json")
		return err
	}

	var errs []error
	for _, arg := range args {
		switch {
		case isMagnet(arg):
			errs = append(errs, queue(map[string]string{"magnet": arg}))
		case strings.EqualFold(filepath.Ext(arg), ".torrent"):
			data, err := os.ReadFile(arg)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			fw, _ := mw.CreateFormFile("file", filepath.Base(arg))
			_, _ = fw.Write(data)
			_ = mw.Close()
			res, err := do("/api/stage", &buf, mw.FormDataContentType())
			if err != nil {
				errs = append(errs, err)
				continue
			}
			var st struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(res, &st); err != nil || st.ID == "" {
				errs = append(errs, fmt.Errorf("%s: malformed reply", arg))
				continue
			}
			errs = append(errs, queue(map[string]string{"stage": st.ID}))
		}
	}
	return errors.Join(errs...)
}
