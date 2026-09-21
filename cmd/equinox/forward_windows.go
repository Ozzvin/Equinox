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

	"github.com/Ozzvin/equinox/internal/app"
)

// isMagnet reports whether a command-line argument is a magnet link.
func isMagnet(s string) bool { return strings.HasPrefix(strings.ToLower(s), "magnet:") }

// addArgs adds magnet links and .torrent files given on the command line (this is how
// Windows hands them over when the app is the registered handler).
func addArgs(a *app.App, args []string) {
	for _, arg := range args {
		var err error
		if isMagnet(arg) {
			_, err = a.Manager.AddMagnet(arg)
		} else if strings.EqualFold(filepath.Ext(arg), ".torrent") {
			_, err = a.Manager.AddFile(arg)
		} else {
			continue
		}
		if err != nil {
			logf("add %q: %v", arg, err)
		}
	}
}

// forward hands command-line arguments to the already running instance through its local
// API. The instance's address and access key are in ui-url next to the settings.
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
	do := func(body io.Reader, contentType string) error {
		req, err := http.NewRequest("POST", base+"/api/torrents", body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", contentType)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			return fmt.Errorf("%s: %s", res.Status, b)
		}
		return nil
	}

	var errs []error
	for _, arg := range args {
		switch {
		case isMagnet(arg):
			b, _ := json.Marshal(map[string]string{"magnet": arg})
			errs = append(errs, do(bytes.NewReader(b), "application/json"))
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
			errs = append(errs, do(&buf, mw.FormDataContentType()))
		}
	}
	return errors.Join(errs...)
}
