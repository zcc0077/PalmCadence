package catalog

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type accountTransport func(*http.Request) (*http.Response, error)

func (f accountTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestPersonalCatalogAdapters(t *testing.T) {
	for _, source := range []string{"tencent", "kuwo", "kugou"} {
		t.Run(source, func(t *testing.T) {
			const image = "\x89PNG\x0d\x0a\x1a\x0a"
			const qqSong = "004TestSongMiD"
			const kgSong = "0123456789abcdef0123456789abcdef"
			var poll int
			client := &http.Client{Transport: accountTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Scheme != "https" {
					t.Fatalf("insecure authentication request to %s", req.URL.Host)
				}
				header := make(http.Header)
				response := ""
				switch req.URL.Path {
				case "/connect/qrconnect":
					response = `<img src="/connect/qrcode/test_qr_ticket">`
				case "/connect/qrcode/test_qr_ticket":
					response = image
				case "/connect/l/qrconnect":
					poll++
					switch poll {
					case 1:
						response = "window.wx_errcode=408;window.wx_code='';"
					case 2:
						response = "window.wx_errcode=404;window.wx_code='';"
					default:
						response = "window.wx_errcode=405;window.wx_code='approved_test_code';"
					}
				case "/v2/qrcode":
					response = `{"status":1,"data":{"qrcode":"test_qr_ticket"}}`
				case "/v2/get_userinfo_qrcode":
					poll++
					if poll == 1 {
						response = `{"status":1,"data":{"status":1}}`
					} else if poll == 2 {
						response = `{"status":1,"data":{"status":2}}`
					} else {
						response = `{"status":1,"data":{"status":4,"userid":"42","token":"test_kg_session","nickname":"测试账号"}}`
					}
				case "/US/platform/WeixinCallback.jsp":
					if req.URL.Query().Get("code") != "approved_test_code" {
						t.Fatal("Kuwo callback missing user-approved code")
					}
					header.Add("Set-Cookie", "uid=42; Secure; HttpOnly")
					header.Add("Set-Cookie", "sid=test_kw_session; Secure; HttpOnly")
					header.Add("Set-Cookie", "nickname=TestAccount; Secure")
					response = "ok"
				case "/cgi-bin/musicu.fcg":
					var in struct {
						Comm map[string]any
						Call struct {
							Method string
							Param  map[string]any
						} `json:"req_0"`
					}
					if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
						t.Fatal(err)
					}
					if in.Call.Method != "Login" && in.Comm["authst"] != "test_qq_session" {
						t.Fatal("missing QQ Music authorization")
					}
					data := ""
					switch in.Call.Method {
					case "Login":
						if in.Call.Param["code"] != "approved_test_code" {
							t.Fatal("QQ exchange missing user-approved code")
						}
						data = `{"musicid":0,"str_musicid":"1000123456789012345","musickey":"test_qq_session","encryptUin":"test_id","nick":"测试账号"}`
					case "GetPlaylistByUin":
						data = `{"v_playlist":[{"tid":15,"dirName":"收藏","songNum":1}]}`
					case "CgiGetPlaylistFavInfo":
						data = `{"v_list":[],"bFinish":true,"total":0}`
					case "CgiGetDiss":
						data = `{"songlist":[{"songInfo":{"mid":"` + qqSong + `","name":"曲目"}}]}`
					case "UrlGetVkey":
						data = `{"midurlinfo":[{"purl":"track.mp3?vkey=test","vkey":"test"}]}`
					default:
						t.Fatalf("unexpected QQ RPC %s", in.Call.Method)
					}
					response = `{"code":0,"req_0":{"code":0,"data":` + data + `}}`
				case "/v8/fcg-bin/fcg_play_single_song.fcg":
					response = `{"data":[{"mid":"` + qqSong + `","file":{"media_mid":"TESTMEDIAID"}}]}`
				case "/v7/get_all_list":
					if req.URL.Query().Get("token") != "test_kg_session" {
						t.Fatal("missing KuGou authorization")
					}
					response = `{"status":1,"data":{"info":[{"listid":"15","name":"收藏","count":1}]}}`
				case "/v4/get_list_all_file_v3":
					response = `{"status":1,"data":{"count":1,"info":[{"hash":"` + kgSong + `","filename":"歌手 - 曲目"}]}}`
				case "/v6/priv_url":
					if req.URL.Query().Get("token") != "test_kg_session" {
						t.Fatal("missing KuGou playback authorization")
					}
					response = `{"status":1,"data":{"info":[{"url":["https://m.kugou.com/test.mp3"]}]}}`
				case "/app/i/getSongInfo.php":
					if value, _ := req.Cookie("t"); value == nil || value.Value != "test_kg_session" {
						t.Fatal("missing KuGou web session")
					}
					response = `{"status":1,"url":"https://m.kugou.com/test.mp3"}`
				case "/pl.svc":
					if req.URL.Query().Get("sid") != "test_kw_session" {
						t.Fatal("missing Kuwo session")
					}
					if req.URL.Query().Get("op") == "pl3_getuserlists" {
						response = `{"errcode":"0","uid":"42","plist":[{"id":"15","title":"收藏","type":"MYFAVORITE","musicnum":"1"}]}`
					} else {
						response = `{"errcode":"0","uid":"42","pid":"15","pagenum":1,"info":{"musiclist":[{"id":67,"name":"曲目"}]}}`
					}
				case "/":
					header.Set("Set-Cookie", "Hm_Iuvt_test=example; Secure")
					response = "guest setup"
				case "/api/v1/www/music/playUrl":
					if value, _ := req.Cookie("sid"); value == nil || value.Value != "test_kw_session" {
						t.Fatal("missing Kuwo playback session")
					}
					response = `{"code":200,"data":{"url":"https://www.kuwo.cn/test.mp3"}}`
				default:
					t.Fatalf("unexpected request %s%s", req.URL.Host, req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(response))}, nil
			})}
			c := Catalog{Client: client, AccountClient: client}
			adapter := c.Account(source)
			flow, err := adapter.Start(t.Context())
			if err != nil || flow.Key == "" {
				t.Fatalf("start: %v", err)
			}
			var secret string
			for _, want := range []int{801, 802, 803} {
				code, value, err := adapter.Poll(t.Context(), flow)
				if err != nil || code != want {
					t.Fatalf("poll %d: got %d %v", want, code, err)
				}
				secret = value
			}
			profile, err := adapter.Profile(t.Context(), secret)
			if err != nil || profile.ID == "" {
				t.Fatalf("profile: %v", err)
			}
			library, err := adapter.Playlists(t.Context(), secret, profile.ID, 1)
			if err != nil || len(library.Playlists) != 1 {
				t.Fatalf("library: %+v %v", library, err)
			}
			tracks, err := adapter.Tracks(t.Context(), secret, string(library.Playlists[0].ID), 1)
			if err != nil || len(tracks.Tracks) != 1 || tracks.Tracks[0].Source != source {
				t.Fatalf("tracks: %+v %v", tracks, err)
			}
			media, err := adapter.Play(t.Context(), secret, tracks.Tracks[0].ID)
			if err != nil || media == nil || media.Extension != "mp3" {
				t.Fatalf("media: %v %v", media, err)
			}
		})
	}
}
