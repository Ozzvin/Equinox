package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A .torrent file can be added by a link that leads straight to it: the program downloads it, and from there it is
// like a file that was put on the list.

const (
	maxFetchedTorrent = 8 << 20 // as big as a .torrent file given to the program by hand may be
	fetchTimeout      = 30 * time.Second
	maxRedirects      = 5
)

// allowLocalFetch lets the link lead to this very computer. It is off: the program does what the person says, but a
// link must not make it reach for what only this computer can see (the program's own interface, a service of the
// system). Only the tests turn it on.
var allowLocalFetch bool

// AllowLocalFetches is for tests: the links may lead to this computer.
func AllowLocalFetches(on bool) { allowLocalFetch = on }

// refuseAddress is the check of every address a download is about to connect to (a redirect is checked too, and
// the address the name was really resolved to): this computer itself and the addresses of the link-local range
// (where the cloud services keep their secrets) are refused. The private networks of the home are not: a
// tracker or a file server there is a usual place for a .torrent.
func refuseAddress(network, address string, _ syscall.RawConn) error {
	if allowLocalFetch {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil
	}
	ip := ap.Addr().Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return &CodedError{Code: "url.blocked", Msg: "ссылка ведёт на этот же компьютер или на служебный адрес: так добавлять нельзя"}
	}
	return nil
}

func fetchClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: refuseAddress}
	return &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        1,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return &CodedError{Code: "url.redirects", Msg: "слишком много перенаправлений"}
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return &CodedError{Code: "url.invalid", Msg: "ссылка должна начинаться с http:// или https://"}
			}
			return nil
		},
	}
}

// FetchTorrent downloads the file a link leads to. It does not look inside: that is for metainfo.Load.
func FetchTorrent(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, &CodedError{Code: "url.invalid", Args: []string{link}, Msg: "ссылка должна начинаться с http:// или https://", Err: ErrInvalidInput}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &CodedError{Code: "url.invalid", Args: []string{link}, Msg: "ссылка должна начинаться с http:// или https://", Err: ErrInvalidInput}
	}
	req.Header.Set("User-Agent", "Equinox")
	req.Header.Set("Accept", "application/x-bittorrent, */*")
	resp, err := fetchClient().Do(req)
	if err != nil {
		var ce *CodedError
		if errors.As(err, &ce) {
			return nil, &CodedError{Code: ce.Code, Args: ce.Args, Msg: ce.Msg, Err: ErrInvalidInput}
		}
		return nil, &CodedError{Code: "url.fetch", Args: []string{err.Error()}, Msg: "не удалось скачать файл по ссылке: " + err.Error(), Err: ErrInvalidInput}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		st := strconv.Itoa(resp.StatusCode)
		return nil, &CodedError{Code: "url.status", Args: []string{st}, Msg: "сайт ответил кодом " + st + " вместо файла", Err: ErrInvalidInput}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchedTorrent+1))
	if err != nil {
		return nil, &CodedError{Code: "url.fetch", Args: []string{err.Error()}, Msg: "не удалось скачать файл по ссылке: " + err.Error(), Err: ErrInvalidInput}
	}
	if len(raw) > maxFetchedTorrent {
		return nil, &CodedError{Code: "url.too_large", Args: []string{fmt.Sprint(maxFetchedTorrent >> 20)}, Msg: "файл по ссылке больше " + fmt.Sprint(maxFetchedTorrent>>20) + " МиБ: это не .torrent", Err: ErrInvalidInput}
	}
	return raw, nil
}
