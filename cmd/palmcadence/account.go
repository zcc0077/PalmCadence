package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/zcc0077/PalmCadence/internal/catalog"
)

const (
	accountFile = "netease.json"
	qrLifetime  = 3 * time.Minute
)

var errLoginBusy = errors.New("too many login attempts")

type loginFlow struct {
	providerKey string
	cookie      string
	until       time.Time
	polling     bool
}

type neteaseSession struct {
	TokenHash string                 `json:"tokenHash"`
	Cookie    string                 `json:"musicU"`
	Profile   catalog.NetEaseProfile `json:"profile"`
}

type accountStore struct {
	dir     string
	mu      sync.Mutex
	session neteaseSession
	flows   map[string]*loginFlow
}

type loginStatus struct {
	State   string                  `json:"state"`
	Profile *catalog.NetEaseProfile `json:"profile,omitempty"`
}

func openAccountStore(dir string) (*accountStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("account directory must be private")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".session-") && file.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, file.Name()))
		}
	}
	store := &accountStore{dir: dir, flows: make(map[string]*loginFlow)}
	data, err := os.ReadFile(filepath.Join(dir, accountFile))
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var saved neteaseSession
	if len(data) <= 8192 && json.Unmarshal(data, &saved) == nil && isAccountToken(saved.TokenHash) &&
		catalog.ValidNetEaseLogin(saved.Cookie) && saved.Profile.ID > 0 {
		store.session = saved
	}
	return store, nil
}

func isAccountToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func accountHash(token string) string {
	if !isAccountToken(token) {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
}

func (a *accountStore) current(hash string) neteaseSession {
	if a == nil || hash == "" {
		return neteaseSession{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(hash), []byte(a.session.TokenHash)) == 1 {
		return a.session
	}
	return neteaseSession{}
}

func (a *accountStore) save(session neteaseSession) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(a.dir, ".session-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), filepath.Join(a.dir, accountFile)); err != nil {
		return err
	}
	a.session = session
	return nil
}

func (a *accountStore) forget(hash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.flows, hash)
	if hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(a.session.TokenHash)) != 1 {
		return nil
	}
	err := os.Remove(filepath.Join(a.dir, accountFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	a.session = neteaseSession{}
	return nil
}

func (a *accountStore) begin(ctx context.Context, provider catalog.Catalog) (string, string, error) {
	var random [32]byte
	rand.Read(random[:])
	token := hex.EncodeToString(random[:])
	hash := accountHash(token)
	now := time.Now()
	flow := &loginFlow{until: now.Add(qrLifetime)}
	a.mu.Lock()
	for name, pending := range a.flows {
		if !pending.until.After(now) {
			delete(a.flows, name)
		}
	}
	if len(a.flows) >= 4 {
		a.mu.Unlock()
		return "", "", errLoginBusy
	}
	a.flows[hash] = flow
	a.mu.Unlock()

	key, err := provider.NetEaseQRStart(ctx)
	var png []byte
	if err == nil {
		link := "https://music.163.com/login?" + url.Values{"codekey": {key}}.Encode()
		png, err = qrcode.Encode(link, qrcode.Medium, 180)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		delete(a.flows, hash)
		return "", "", err
	}
	flow.providerKey = key
	return token, "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func signedIn(profile catalog.NetEaseProfile) loginStatus {
	return loginStatus{State: "signed_in", Profile: &profile}
}

func (a *accountStore) poll(ctx context.Context, provider catalog.Catalog, hash string) (loginStatus, error) {
	a.mu.Lock()
	if hash != "" && subtle.ConstantTimeCompare([]byte(hash), []byte(a.session.TokenHash)) == 1 {
		profile := a.session.Profile
		a.mu.Unlock()
		return signedIn(profile), nil
	}
	flow := a.flows[hash]
	if flow == nil || !flow.until.After(time.Now()) {
		delete(a.flows, hash)
		a.mu.Unlock()
		return loginStatus{State: "expired"}, nil
	}
	if flow.polling || flow.providerKey == "" {
		a.mu.Unlock()
		return loginStatus{State: "waiting"}, nil
	}
	flow.polling = true
	key, cookie := flow.providerKey, flow.cookie
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		flow.polling = false
		a.mu.Unlock()
	}()

	if cookie == "" {
		code, grant, err := provider.NetEaseQRPoll(ctx, key)
		if err != nil {
			return loginStatus{}, err
		}
		switch code {
		case 800:
			a.mu.Lock()
			delete(a.flows, hash)
			a.mu.Unlock()
			return loginStatus{State: "expired"}, nil
		case 801:
			return loginStatus{State: "waiting"}, nil
		case 802:
			return loginStatus{State: "confirming"}, nil
		case 803:
			cookie = grant
			a.mu.Lock()
			flow.cookie = grant
			a.mu.Unlock()
		default:
			return loginStatus{}, fmt.Errorf("NetEase QR poll code %d", code)
		}
	}

	profile, err := provider.NetEaseAccount(ctx, cookie)
	if err != nil {
		return loginStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flows[hash] != flow || !flow.until.After(time.Now()) {
		return loginStatus{State: "expired"}, nil
	}
	if err := a.save(neteaseSession{TokenHash: hash, Cookie: cookie, Profile: profile}); err != nil {
		return loginStatus{}, err
	}
	delete(a.flows, hash)
	return signedIn(profile), nil
}

func (s *appServer) serveAccount(w http.ResponseWriter, req *http.Request, action string) {
	if s.account == nil {
		fail(w, http.StatusServiceUnavailable, "此设备的本机账号存储暂不可用。")
		return
	}
	if req.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "请求方式不受支持。")
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 1024))
	if err != nil {
		fail(w, http.StatusBadRequest, "账号请求无效。")
		return
	}
	form, err := url.ParseQuery(string(data))
	if err != nil {
		fail(w, http.StatusBadRequest, "账号请求无效。")
		return
	}
	hash := accountHash(form.Get("token"))
	ctx := req.Context()
	switch action {
	case "start":
		token, image, err := s.account.begin(ctx, s.catalog)
		if errors.Is(err, errLoginBusy) {
			fail(w, http.StatusTooManyRequests, "扫码请求过多，请稍后重试。")
			return
		}
		if err != nil {
			logFailure("netease-login", err)
			fail(w, http.StatusBadGateway, "暂时拿不到登录码，请重试。")
			return
		}
		respond(w, http.StatusOK, map[string]string{"token": token, "image": image})
	case "poll":
		status, err := s.account.poll(ctx, s.catalog, hash)
		if err != nil {
			logFailure("netease-login", err)
			fail(w, http.StatusBadGateway, "暂时无法确认登录。")
			return
		}
		respond(w, http.StatusOK, status)
	case "status":
		if session := s.account.current(hash); session.Cookie != "" {
			respond(w, http.StatusOK, signedIn(session.Profile))
		} else {
			respond(w, http.StatusOK, loginStatus{State: "signed_out"})
		}
	case "logout":
		if err := s.account.forget(hash); err != nil {
			logFailure("netease-logout", err)
			fail(w, http.StatusInternalServerError, "暂时无法退出登录，请重试。")
			return
		}
		respond(w, http.StatusOK, loginStatus{State: "signed_out"})
	case "playlists", "tracks", "play":
		s.servePersonalMusic(w, req, action, form, hash)
	default:
		fail(w, http.StatusNotFound, "找不到这个账号操作。")
	}
}

func (s *appServer) servePersonalMusic(w http.ResponseWriter, req *http.Request, action string, args url.Values, hash string) {
	session := s.account.current(hash)
	if session.Cookie == "" {
		fail(w, http.StatusUnauthorized, "请先登录网易云账号。")
		return
	}

	id := args.Get("id")
	page := 1
	if raw := args.Get("page"); raw != "" {
		page, _ = strconv.Atoi(raw)
	}
	if page < 1 || page > 1000 || (action != "playlists" && !catalog.ValidID("netease", id)) {
		fail(w, http.StatusBadRequest, "歌单或曲目编号无效。")
		return
	}

	var result any
	var err error
	ctx := req.Context()
	if action == "playlists" {
		result, err = s.catalog.NetEasePlaylists(ctx, session.Cookie, session.Profile.ID, page)
	} else if action == "tracks" {
		result, err = s.catalog.NetEasePlaylistTracks(ctx, session.Cookie, id, page)
	} else {
		var media *catalog.Media
		media, err = s.catalog.NetEaseAuthorizedMedia(ctx, session.Cookie, id)
		if err == nil && media == nil {
			fail(w, http.StatusNotFound, "平台未给此账号提供可播音源。")
			return
		}
		if err == nil {
			entry := stream{source: "netease", media: media, accountHash: hash}
			result = map[string]string{"path": "/media/" + s.remember(entry, "."+media.Extension)}
		}
	}
	if errors.Is(err, catalog.ErrNetEaseSession) {
		_ = s.account.forget(hash)
		fail(w, http.StatusUnauthorized, "网易云登录已失效，请重新扫码。")
		return
	}
	if err != nil {
		logFailure("netease-account", err)
		fail(w, http.StatusBadGateway, "暂时无法连接网易云，请重试。")
		return
	}
	if s.account.current(hash).Cookie == "" {
		fail(w, http.StatusUnauthorized, "请先登录网易云账号。")
		return
	}
	respond(w, http.StatusOK, result)
}
