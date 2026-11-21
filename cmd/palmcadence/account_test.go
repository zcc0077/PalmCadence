package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zcc0077/PalmCadence/internal/catalog"
)

func TestNetEaseAccountFlowKeepsCredentialsOnDevice(t *testing.T) {
	store, err := openAccountStore(filepath.Join(t.TempDir(), "account"))
	if err != nil {
		t.Fatal(err)
	}
	const providerCookie = "private-provider-credential"
	const qrKey = "test_provider_key"
	polls := 0
	provider := &http.Client{Transport: mediaTransport(func(req *http.Request) (*http.Response, error) {
		t.Helper()
		if req.URL.Scheme != "https" || req.URL.Host != "music.163.com" {
			t.Fatalf("unexpected provider URL %s", req.URL)
		}
		header := make(http.Header)
		body := ""
		cookie, _ := req.Cookie("MUSIC_U")
		switch req.URL.Path {
		case "/eapi/login/qrcode/unikey":
			body = `{"code":200,"unikey":"` + qrKey + `"}`
		case "/eapi/login/qrcode/client/login":
			polls++
			if polls == 1 {
				body = `{"code":801}`
			} else if polls == 2 {
				body = `{"code":802}`
			} else {
				body = `{"code":803}`
				header.Set("Set-Cookie", "MUSIC_U="+providerCookie+"; Path=/; Secure; HttpOnly")
			}
		default:
			if cookie == nil || cookie.Value != providerCookie {
				t.Fatal("authorized request missing the provider cookie")
			}
			switch req.URL.Path {
			case "/eapi/nuser/account/get":
				body = `{"code":200,"profile":{"userId":42,"nickname":"测试用户"}}`
			case "/eapi/user/playlist":
				body = `{"code":200,"more":false,"playlist":[{"id":314,"name":"我的收藏","trackCount":2}]}`
			case "/eapi/v6/playlist/detail":
				body = `{"code":200,"playlist":{"trackIds":[{"id":67},{"id":88}]}}`
			case "/eapi/v3/song/detail":
				body = `{"code":200,"songs":[{"id":88,"name":"第二首"},{"id":67,"name":"第一首"}]}`
			case "/eapi/song/enhance/player/url":
				body = `{"code":200,"data":[{"url":"https://music.126.net/audio.mp3"}]}`
			default:
				t.Fatalf("unexpected provider path %s", req.URL.Path)
			}
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	app := appServer{
		host: "localhost", catalog: catalog.Catalog{Client: provider}, account: store,
		entries: make(map[string]stream), cache: testCache(t),
		media: &http.Client{Transport: mediaTransport(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Cookie") != "" || req.URL.Host != "music.126.net" {
				t.Fatal("account credentials leaked to the media host")
			}
			header := make(http.Header)
			header.Set("Content-Type", "audio/mpeg")
			return &http.Response{StatusCode: 200, Header: header, ContentLength: 4, Body: io.NopCloser(strings.NewReader("song"))}, nil
		})},
	}
	post := func(action, token, id string, want int, contains string) string {
		t.Helper()
		form := url.Values{"token": {token}, "id": {id}}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/account/"+action, strings.NewReader(form.Encode()))
		req.Header.Set("Origin", palmAppOrigin)
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		body := res.Body.String()
		if res.Code != want || res.Header().Get("Access-Control-Allow-Origin") != palmAppOrigin ||
			!strings.Contains(body, contains) || strings.Contains(body, providerCookie) || strings.Contains(body, qrKey) {
			t.Fatalf("%s: %d %s", action, res.Code, body)
		}
		return body
	}
	var start struct{ Token, Image string }
	if err := json.Unmarshal([]byte(post("start", "", "", 200, "data:image/png;base64,")), &start); err != nil || !isAccountToken(start.Token) {
		t.Fatalf("invalid start response: %v", err)
	}
	unknown := strings.Repeat("f", 64)
	post("poll", unknown, "", 200, "expired")
	post("playlists", start.Token, "", 401, "请先登录")
	post("poll", start.Token, "", 200, "waiting")
	post("poll", start.Token, "", 200, "confirming")
	post("poll", start.Token, "", 200, "测试用户")
	post("status", unknown, "", 200, "signed_out")
	app.account, err = openAccountStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	post("status", start.Token, "", 200, "测试用户")
	post("playlists", start.Token, "", 200, "我的收藏")
	tracks := post("tracks", start.Token, "314", 200, "第一首")
	if strings.Index(tracks, "第一首") >= strings.Index(tracks, "第二首") {
		t.Fatalf("playlist order changed: %s", tracks)
	}
	var stream struct{ Path string }
	if err := json.Unmarshal([]byte(post("play", start.Token, "67", 200, "/media/")), &stream); err != nil {
		t.Fatal(err)
	}
	fetch := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+path, nil)
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		return res
	}
	if res := fetch(stream.Path); res.Code != 200 || res.Body.String() != "song" || app.cache.status().Count != 0 {
		t.Fatalf("protected stream response: %d %q; cache: %+v", res.Code, res.Body, app.cache.status())
	}
	post("logout", start.Token, "", 200, "signed_out")
	if res := fetch(stream.Path); res.Code != 401 {
		t.Fatalf("old media handle still active after logout: %d", res.Code)
	}
	post("playlists", start.Token, "", 401, "请先登录")
	reopened, err := openAccountStore(store.dir)
	if err != nil || reopened.current("netease", accountHash(start.Token)).Cookie != "" {
		t.Fatalf("session survived logout: %v", err)
	}
}

func TestAccountStoreKeepsProvidersIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "account")
	store, err := openAccountStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	neteaseHash := accountHash(strings.Repeat("a", 64))
	qqHash := accountHash(strings.Repeat("b", 64))
	netease := accountSession{TokenHash: neteaseHash, Cookie: "netease_example", Profile: catalog.AccountProfile{ID: "19", Name: "网易云"}}
	qq := accountSession{TokenHash: qqHash, Cookie: `{"musicid":"20","musickey":"qq_example"}`, Profile: catalog.AccountProfile{ID: "20", Name: "QQ"}}
	if err := store.save("netease", netease); err != nil {
		t.Fatal(err)
	}
	if err := store.save("tencent", qq); err != nil {
		t.Fatal(err)
	}
	if store.current("tencent", neteaseHash).Cookie != "" {
		t.Fatal("NetEase capability could access QQ")
	}
	if err := store.forget("tencent", neteaseHash); err != nil {
		t.Fatal(err)
	}
	if store.current("tencent", qqHash).Cookie == "" {
		t.Fatal("wrong capability logged out QQ")
	}
	if err := store.forget("tencent", qqHash); err != nil {
		t.Fatal(err)
	}
	reopened, err := openAccountStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.current("netease", neteaseHash) != netease || reopened.current("tencent", qqHash).Cookie != "" {
		t.Fatal("QQ logout was not isolated")
	}
}
