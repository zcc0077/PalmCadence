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
	qrLifetime = 3 * time.Minute
)

var errLoginBusy = errors.New("too many login attempts")

type loginFlow struct {
	challenge catalog.LoginChallenge
	cookie    string
	until     time.Time
	polling   bool
}

type accountSession struct {
	TokenHash string                 `json:"tokenHash"`
	Cookie    string                 `json:"musicU"`
	Profile   catalog.AccountProfile `json:"profile"`
}

type accountStore struct {
	dir      string
	mu       sync.Mutex
	sessions map[string]accountSession
	flows    map[string]*loginFlow
}

type loginStatus struct {
	State   string                  `json:"state"`
	Profile *catalog.AccountProfile `json:"profile,omitempty"`
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
	store := &accountStore{dir: dir, flows: make(map[string]*loginFlow), sessions: make(map[string]accountSession)}
	for _, source := range []string{"netease", "tencent", "kugou", "kuwo"} {
		data, err := os.ReadFile(filepath.Join(dir, source+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var saved accountSession
		if len(data) <= 8192 && json.Unmarshal(data, &saved) == nil && isAccountToken(saved.TokenHash) &&
			catalog.ValidAccountSecret(source, saved.Cookie) && saved.Profile.ID != "" {
			store.sessions[source] = saved
		}
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

func (a *accountStore) current(source, hash string) accountSession {
	if a == nil || hash == "" {
		return accountSession{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(hash), []byte(a.sessions[source].TokenHash)) == 1 {
		return a.sessions[source]
	}
	return accountSession{}
}

func (a *accountStore) save(source string, session accountSession) error {
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
	if err := os.Rename(temp.Name(), filepath.Join(a.dir, source+".json")); err != nil {
		return err
	}
	a.sessions[source] = session
	return nil
}

func (a *accountStore) forget(source, hash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.flows, source+hash)
	if hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(a.sessions[source].TokenHash)) != 1 {
		return nil
	}
	err := os.Remove(filepath.Join(a.dir, source+".json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(a.sessions, source)
	return nil
}

func (a *accountStore) begin(ctx context.Context, source string, provider catalog.AccountProvider) (string, string, error) {
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
	a.flows[source+hash] = flow
	a.mu.Unlock()

	challenge, err := provider.Start(ctx)
	if err == nil && challenge.Image == "" {
		var png []byte
		png, err = qrcode.Encode(challenge.Link, qrcode.Medium, 180)
		challenge.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		delete(a.flows, source+hash)
		return "", "", err
	}
	flow.challenge = challenge
	return token, challenge.Image, nil
}

func signedIn(profile catalog.AccountProfile) loginStatus {
	return loginStatus{State: "signed_in", Profile: &profile}
}

func (a *accountStore) poll(ctx context.Context, source string, provider catalog.AccountProvider, hash string) (loginStatus, error) {
	a.mu.Lock()
	if hash != "" && subtle.ConstantTimeCompare([]byte(hash), []byte(a.sessions[source].TokenHash)) == 1 {
		profile := a.sessions[source].Profile
		a.mu.Unlock()
		return signedIn(profile), nil
	}
	flow := a.flows[source+hash]
	if flow == nil || !flow.until.After(time.Now()) {
		delete(a.flows, source+hash)
		a.mu.Unlock()
		return loginStatus{State: "expired"}, nil
	}
	if flow.polling || flow.challenge.Key == "" {
		a.mu.Unlock()
		return loginStatus{State: "waiting"}, nil
	}
	flow.polling = true
	challenge, cookie := flow.challenge, flow.cookie
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		flow.polling = false
		a.mu.Unlock()
	}()

	if cookie == "" {
		code, grant, err := provider.Poll(ctx, challenge)
		if err != nil {
			return loginStatus{}, err
		}
		switch code {
		case 800, 403:
			a.mu.Lock()
			delete(a.flows, source+hash)
			a.mu.Unlock()
			return loginStatus{State: "expired"}, nil
		case 801:
			return loginStatus{State: "waiting"}, nil
		case 802:
			return loginStatus{State: "confirming"}, nil
		case 803:
			if !catalog.ValidAccountSecret(source, grant) {
				return loginStatus{}, errors.New("provider did not return a valid account session")
			}
			cookie = grant
			a.mu.Lock()
			flow.cookie = grant
			a.mu.Unlock()
		default:
			return loginStatus{}, fmt.Errorf("NetEase QR poll code %d", code)
		}
	}

	profile, err := provider.Profile(ctx, cookie)
	if err != nil {
		return loginStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flows[source+hash] != flow || !flow.until.After(time.Now()) {
		return loginStatus{State: "expired"}, nil
	}
	if err := a.save(source, accountSession{TokenHash: hash, Cookie: cookie, Profile: profile}); err != nil {
		return loginStatus{}, err
	}
	delete(a.flows, source+hash)
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
	source := form.Get("source")
	if source == "" {
		source = "netease"
	}
	provider := s.catalog.Account(source)
	if provider == nil {
		fail(w, http.StatusNotImplemented, "此音源暂不支持在 Palm 上登录。")
		return
	}
	hash := accountHash(form.Get("token"))
	ctx := req.Context()
	switch action {
	case "start":
		token, image, err := s.account.begin(ctx, source, provider)
		if errors.Is(err, errLoginBusy) {
			fail(w, http.StatusTooManyRequests, "扫码请求过多，请稍后重试。")
			return
		}
		if err != nil {
			logFailure(source+"-login", err)
			fail(w, http.StatusBadGateway, "暂时拿不到登录码，请重试。")
			return
		}
		respond(w, http.StatusOK, map[string]string{"token": token, "image": image})
	case "poll":
		status, err := s.account.poll(ctx, source, provider, hash)
		if err != nil {
			logFailure(source+"-login", err)
			fail(w, http.StatusBadGateway, "暂时无法确认登录。")
			return
		}
		respond(w, http.StatusOK, status)
	case "status":
		if session := s.account.current(source, hash); session.Cookie != "" {
			respond(w, http.StatusOK, signedIn(session.Profile))
		} else {
			respond(w, http.StatusOK, loginStatus{State: "signed_out"})
		}
	case "logout":
		if err := s.account.forget(source, hash); err != nil {
			logFailure(source+"-logout", err)
			fail(w, http.StatusInternalServerError, "暂时无法退出登录，请重试。")
			return
		}
		respond(w, http.StatusOK, loginStatus{State: "signed_out"})
	case "playlists", "tracks", "play":
		s.servePersonalMusic(w, req, source, provider, action, form, hash)
	default:
		fail(w, http.StatusNotFound, "找不到这个账号操作。")
	}
}

func (s *appServer) servePersonalMusic(w http.ResponseWriter, req *http.Request, source string, provider catalog.AccountProvider, action string, args url.Values, hash string) {
	session := s.account.current(source, hash)
	if session.Cookie == "" {
		fail(w, http.StatusUnauthorized, "请先登录当前音源账号。")
		return
	}

	id := args.Get("id")
	page := 1
	if raw := args.Get("page"); raw != "" {
		page, _ = strconv.Atoi(raw)
	}
	if page < 1 || page > 1000 || (action == "tracks" && !catalog.ValidPlaylistID(source, id)) || (action == "play" && !catalog.ValidID(source, id)) {
		fail(w, http.StatusBadRequest, "歌单或曲目编号无效。")
		return
	}

	var result any
	var err error
	ctx := req.Context()
	if action == "playlists" {
		result, err = provider.Playlists(ctx, session.Cookie, session.Profile.ID, page)
	} else if action == "tracks" {
		result, err = provider.Tracks(ctx, session.Cookie, id, page)
	} else {
		var media *catalog.Media
		media, err = provider.Play(ctx, session.Cookie, id)
		if err == nil && media == nil {
			fail(w, http.StatusNotFound, "平台未给此账号提供可播音源。")
			return
		}
		if err == nil {
			entry := stream{source: source, media: media, accountHash: hash}
			result = map[string]string{"path": "/media/" + s.remember(entry, "."+media.Extension)}
		}
	}
	if errors.Is(err, catalog.ErrAccountExpired) {
		_ = s.account.forget(source, hash)
		fail(w, http.StatusUnauthorized, "登录已失效，请重新扫码。")
		return
	}
	if err != nil {
		logFailure(source+"-account", err)
		fail(w, http.StatusBadGateway, "暂时无法连接音源，请重试。")
		return
	}
	if s.account.current(source, hash).Cookie == "" {
		fail(w, http.StatusUnauthorized, "请先登录当前音源账号。")
		return
	}
	respond(w, http.StatusOK, result)
}
