package catalog

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type kugouAccount struct{ Catalog }

type kugouLogin struct {
	User     AccountID `json:"userid"`
	Token    string    `json:"token"`
	Nickname string    `json:"nickname"`
	MID      string    `json:"mid"`
}

const kugouWebSalt = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
const kugouAppSalt = "OIlwieks28dk2k092lksi2UIkp"

func (a kugouAccount) request(ctx context.Context, endpoint, route, mid, salt string, args url.Values, payload any, result any) error {
	if args == nil {
		args = make(url.Values)
	}
	clock := strconv.FormatInt(time.Now().Unix(), 10)
	args.Set("dfid", "-")
	args.Set("mid", mid)
	args.Set("uuid", "-")
	if args.Get("appid") == "" {
		args.Set("appid", "1005")
	}
	args.Set("clientver", "20489")
	args.Set("clienttime", clock)
	method := http.MethodGet
	body := ""
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = string(data)
		method = http.MethodPost
	}
	keys := make([]string, 0, len(args))
	for name := range args {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString(salt)
	for _, name := range keys {
		text.WriteString(name + "=" + args.Get(name))
	}
	text.WriteString(body)
	text.WriteString(salt)
	args.Set("signature", fmt.Sprintf("%x", md5.Sum([]byte(text.String()))))
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"?"+args.Encode(), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Android15-1070-11083-46-0-DiscoveryDRADProtocol-wifi")
	req.Header.Set("mid", mid)
	req.Header.Set("dfid", "-")
	req.Header.Set("clienttime", clock)
	req.Header.Set("kg-rc", "1")
	req.Header.Set("kg-thash", "5d816a0")
	req.Header.Set("kg-rec", "1")
	req.Header.Set("kg-rf", "B9EDA08A64250DEFFBCADDEE00F8F25F")
	if route != "" {
		req.Header.Set("x-router", route)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	data, err := a.authCatalog().readCatalog(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func (a kugouAccount) Start(ctx context.Context) (LoginChallenge, error) {
	mid := randomID()
	var result struct {
		Status int
		Data   struct{ QRCode string }
	}
	args := url.Values{"appid": {"1001"}, "type": {"1"}, "plat": {"4"}, "srcappid": {"2919"}, "qrcode_txt": {"https://h5.kugou.com/apps/loginQRCode/html/index.html?appid=1005&"}}
	err := a.request(ctx, "https://login-user.kugou.com/v2/qrcode", "", mid, kugouWebSalt, args, nil, &result)
	if err != nil {
		return LoginChallenge{}, err
	}
	if result.Status != 1 || !authKey.MatchString(result.Data.QRCode) {
		return LoginChallenge{}, fmt.Errorf("KuGou did not issue a QR ticket")
	}
	link := "https://h5.kugou.com/apps/loginQRCode/html/index.html?qrcode=" + url.QueryEscape(result.Data.QRCode)
	return LoginChallenge{Key: result.Data.QRCode, State: mid, Link: link}, nil
}

func (a kugouAccount) Poll(ctx context.Context, challenge LoginChallenge) (int, string, error) {
	var reply struct {
		Status int
		Data   *struct {
			Status int
			kugouLogin
		}
	}
	args := url.Values{"plat": {"4"}, "srcappid": {"2919"}, "qrcode": {challenge.Key}}
	err := a.request(ctx, "https://login-user.kugou.com/v2/get_userinfo_qrcode", "", challenge.State, kugouWebSalt, args, nil, &reply)
	if err != nil {
		return 0, "", err
	}
	if reply.Data == nil {
		return 0, "", fmt.Errorf("KuGou QR poll status %d", reply.Status)
	}
	switch reply.Data.Status {
	case 0:
		return 800, "", nil
	case 1:
		return 801, "", nil
	case 2:
		return 802, "", nil
	case 4:
		grant := reply.Data.kugouLogin
		grant.MID = challenge.State
		if !numericID.MatchString(string(grant.User)) || grant.Token == "" {
			return 0, "", fmt.Errorf("KuGou confirmation is missing the account session")
		}
		secret, err := encodeAccountSecret(grant)
		return 803, secret, err
	default:
		return 0, "", fmt.Errorf("KuGou QR poll returned status %d", reply.Data.Status)
	}
}

func (a kugouAccount) Profile(_ context.Context, secret string) (AccountProfile, error) {
	grant, err := decodeAccountSecret[kugouLogin](secret)
	return AccountProfile{grant.User, clean(grant.Nickname)}, err
}

type kugouReply struct {
	Status    int
	ErrCode   int
	ErrorCode int `json:"error_code"`
}

func (reply kugouReply) check() error {
	if reply.Status == 1 && reply.ErrorCode == 0 && reply.ErrCode == 0 {
		return nil
	}
	if reply.ErrCode == 20010 || reply.ErrorCode == 20010 || reply.ErrCode == 401 {
		return ErrAccountExpired
	}
	return fmt.Errorf("KuGou returned %d/%d/%d", reply.Status, reply.ErrCode, reply.ErrorCode)
}

func (a kugouAccount) Playlists(ctx context.Context, secret string, _ AccountID, page int) (PersonalLibrary, error) {
	grant, err := decodeAccountSecret[kugouLogin](secret)
	if err != nil {
		return PersonalLibrary{}, err
	}
	args := url.Values{"userid": {string(grant.User)}, "token": {grant.Token}, "plat": {"1"}}
	payload := map[string]any{"userid": grant.User, "token": grant.Token, "total_ver": 979, "type": 2, "page": page, "pagesize": PageSize}
	var reply struct {
		kugouReply
		Data struct {
			Total int
			Info  []struct {
				ID                AccountID `json:"listid"`
				Name, SpecialName string
				SongCount, Count  int
			}
		}
	}
	if err := a.request(ctx, "https://gateway.kugou.com/v7/get_all_list", "cloudlist.service.kugou.com", grant.MID, kugouAppSalt, args, payload, &reply); err != nil {
		return PersonalLibrary{}, err
	}
	if err := reply.check(); err != nil {
		return PersonalLibrary{}, err
	}
	library := PersonalLibrary{Playlists: []PersonalPlaylist{}, More: reply.Data.Total > page*PageSize || len(reply.Data.Info) == PageSize}
	for _, item := range reply.Data.Info {
		name := item.Name
		if name == "" {
			name = item.SpecialName
		}
		if ValidPlaylistID("kugou", string(item.ID)) && clean(name) != "" {
			library.Playlists = append(library.Playlists, PersonalPlaylist{item.ID, clean(name), max(item.SongCount, item.Count)})
		}
	}
	return library, nil
}

func (a kugouAccount) Tracks(ctx context.Context, secret, id string, page int) (Results, error) {
	grant, err := decodeAccountSecret[kugouLogin](secret)
	if err != nil {
		return Results{}, err
	}
	args := url.Values{"userid": {string(grant.User)}, "token": {grant.Token}}
	payload := map[string]any{"listid": id, "userid": grant.User, "token": grant.Token, "type": 0, "page": page, "pagesize": PageSize, "area_code": 1, "allplatform": 1, "show_cover": 1}
	var reply struct {
		kugouReply
		Data struct {
			Count int
			Info  []struct{ Hash, FileHash, Name, FileName, AlbumName string }
		}
	}
	if err := a.request(ctx, "https://gateway.kugou.com/v4/get_list_all_file_v3", "cloudlist.service.kugou.com", grant.MID, kugouAppSalt, args, payload, &reply); err != nil {
		return Results{}, err
	}
	if err := reply.check(); err != nil {
		return Results{}, err
	}
	out := Results{Tracks: []Track{}, More: reply.Data.Count > page*PageSize || len(reply.Data.Info) == PageSize}
	for _, row := range reply.Data.Info {
		hash := row.Hash
		if hash == "" {
			hash = row.FileHash
		}
		name := row.Name
		if name == "" {
			name = row.FileName
		}
		artist, title := splitKugouName(name)
		out.add("kugou", hash, title, artist, row.AlbumName)
	}
	return out, nil
}

func splitKugouName(name string) (string, string) {
	artist, title, found := strings.Cut(name, " - ")
	if !found {
		return "", name
	}
	return artist, title
}

func (a kugouAccount) Play(ctx context.Context, secret, id string) (*Media, error) {
	grant, err := decodeAccountSecret[kugouLogin](secret)
	if err != nil {
		return nil, err
	}
	media, nativeErr := a.playKugouAccount(ctx, grant, id)
	if media != nil || nativeErr == ErrAccountExpired {
		return media, nativeErr
	}
	args := url.Values{"cmd": {"playInfo"}, "hash": {id}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://m.kugou.com/app/i/getSongInfo.php?"+args.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://m.kugou.com/")
	req.Header.Set("User-Agent", authWebAgent)
	req.AddCookie(&http.Cookie{Name: "KugooID", Value: string(grant.User)})
	req.AddCookie(&http.Cookie{Name: "t", Value: grant.Token})
	body, err := a.authCatalog().readCatalog(req)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status int
		URL    string
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Status != 1 {
		return nil, nativeErr
	}
	return parseMedia("kugou", result.URL, nil)
}

func (a kugouAccount) playKugouAccount(ctx context.Context, grant kugouLogin, id string) (*Media, error) {
	key := fmt.Sprintf("%x", md5.Sum([]byte(id+"185672dd44712f60bb1736df5a377e82"+"1005"+grant.MID+string(grant.User))))
	args := url.Values{"userid": {string(grant.User)}, "token": {grant.Token}}
	payload := map[string]any{
		"area_code": "1", "behavior": "play", "qualities": []string{"128"}, "token": grant.Token, "userid": grant.User,
		"resource":      map[string]any{"hash": id, "type": "audio", "id": 0, "page_id": 1},
		"tracker_param": map[string]any{"key": key, "all_m": 1, "module_id": 0, "is_free_part": 0, "pid": "411", "pidversion": "3001"},
	}
	var reply struct {
		kugouReply
		Data json.RawMessage
	}
	if err := a.request(ctx, "https://gateway.kugou.com/v6/priv_url", "tracker.kugou.com", grant.MID, kugouAppSalt, args, payload, &reply); err != nil {
		return nil, err
	}
	if err := reply.check(); err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal(reply.Data, &data); err != nil {
		return nil, err
	}
	return kugouStreamIn(data), nil
}

func kugouStreamIn(node any) *Media {
	switch value := node.(type) {
	case string:
		stream, _ := parseMedia("kugou", value, nil)
		return stream
	case []any:
		for _, item := range value {
			if stream := kugouStreamIn(item); stream != nil {
				return stream
			}
		}
	case map[string]any:
		for _, name := range []string{"url", "urls", "backup_url", "play_url", "playUrl", "url_list", "location", "info", "resources", "qualities", "data"} {
			if stream := kugouStreamIn(value[name]); stream != nil {
				return stream
			}
		}
	}
	return nil
}
