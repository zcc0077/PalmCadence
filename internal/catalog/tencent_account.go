package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const qqWechatID = "wx48db31d50e334801"
const qqWechatRedirect = "https://y.qq.com/portal/wx_redirect.html?login_type=2&surl=https://y.qq.com/"

type qqAccount struct{ Catalog }

type qqGrant struct {
	User          AccountID `json:"musicid"`
	StringUser    AccountID `json:"str_musicid"`
	Key           string    `json:"musickey"`
	EncryptedUser string    `json:"encryptUin"`
	LoginType     int       `json:"loginType"`
	Nickname      string    `json:"nick"`
	Device        string    `json:"device"`
}

func (g qqGrant) user() AccountID {
	if g.StringUser != "" && g.StringUser != "0" {
		return g.StringUser
	}
	return g.User
}

func (a qqAccount) Start(ctx context.Context) (LoginChallenge, error) {
	return a.startWeChat(ctx, qqWechatID, qqWechatRedirect)
}

func (a qqAccount) Poll(ctx context.Context, challenge LoginChallenge) (int, string, error) {
	code, authorization, err := a.pollWeChat(ctx, challenge.Key)
	if code != 803 || err != nil {
		return code, "", err
	}
	grant := qqGrant{Device: challenge.State, LoginType: 1}
	identity := grant.Device
	if err := a.rpc(ctx, grant, "music.login.LoginServer", "Login", map[string]any{"code": authorization, "strAppid": qqWechatID}, &grant); err != nil {
		return 0, "", err
	}
	grant.Device = identity
	if grant.LoginType == 0 {
		grant.LoginType = 1
	}
	if grant.Key == "" || grant.user() == "" || grant.user() == "0" {
		return 0, "", fmt.Errorf("QQ Music confirmation is missing the account session")
	}
	secret, err := encodeAccountSecret(grant)
	return 803, secret, err
}

func (a qqAccount) rpc(ctx context.Context, grant qqGrant, module, method string, params any, out any) error {
	comm := map[string]any{"ct": 11, "cv": 14090008, "v": 14090008, "chid": "10003505", "tmeAppID": "qqmusic", "tmeLoginType": grant.LoginType,
		"OpenUDID": grant.Device, "udid": grant.Device, "OpenUDID2": grant.Device, "os_ver": "14", "devicelevel": "34", "newdevicelevel": "34"}
	if grant.Key != "" {
		comm["qq"] = grant.user()
		comm["authst"] = grant.Key
	}
	data, err := json.Marshal(map[string]any{"comm": comm, "req_0": map[string]any{"module": module, "method": method, "param": params}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://u.y.qq.com/cgi-bin/musicu.fcg", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", qqReferer)
	req.Header.Set("User-Agent", "QQMusic 14090008(android 14)")
	if grant.Key != "" {
		req.AddCookie(&http.Cookie{Name: "uin", Value: string(grant.user())})
		req.AddCookie(&http.Cookie{Name: "qqmusic_key", Value: grant.Key})
	}
	body, err := a.authCatalog().readCatalog(req)
	if err != nil {
		return err
	}
	var result struct {
		Code  int
		Reply *struct {
			Code int
			Data json.RawMessage
		} `json:"req_0"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if result.Reply == nil {
		return fmt.Errorf("QQ Music returned no RPC result (%d)", result.Code)
	}
	if result.Code != 0 || result.Reply.Code != 0 {
		if grant.Key != "" && (result.Code == 401 || result.Reply.Code == 401 || result.Reply.Code == 1003 || result.Reply.Code == 20002) {
			return ErrAccountExpired
		}
		return fmt.Errorf("QQ Music returned %d/%d", result.Code, result.Reply.Code)
	}
	return json.Unmarshal(result.Reply.Data, out)
}

func (a qqAccount) Profile(_ context.Context, secret string) (AccountProfile, error) {
	grant, err := decodeAccountSecret[qqGrant](secret)
	return AccountProfile{grant.user(), clean(grant.Nickname)}, err
}

type qqListItem struct {
	ID        AccountID `json:"tid"`
	DirName   string    `json:"dirName"`
	Name      string    `json:"name"`
	SongCount int       `json:"songNum"`
}

func (a qqAccount) Playlists(ctx context.Context, secret string, _ AccountID, page int) (PersonalLibrary, error) {
	grant, err := decodeAccountSecret[qqGrant](secret)
	if err != nil {
		return PersonalLibrary{}, err
	}
	var owned struct {
		Playlists []qqListItem `json:"v_playlist"`
	}
	if err := a.rpc(ctx, grant, "music.musicasset.PlaylistBaseRead", "GetPlaylistByUin", map[string]any{"uin": grant.user()}, &owned); err != nil {
		return PersonalLibrary{}, err
	}
	out := make([]PersonalPlaylist, 0, len(owned.Playlists)+PageSize)
	add := func(item qqListItem) {
		name := item.DirName
		if name == "" {
			name = item.Name
		}
		if ValidPlaylistID("tencent", string(item.ID)) && clean(name) != "" {
			out = append(out, PersonalPlaylist{item.ID, clean(name), item.SongCount})
		}
	}
	for _, item := range owned.Playlists {
		add(item)
	}
	first := (page - 1) * PageSize
	if len(out) > first+PageSize || grant.EncryptedUser == "" {
		return takePlaylists(out, page), nil
	}
	// Read only enough saved lists for this page and keep owned lists first.
	remaining := max(0, first-len(out))
	var saved struct {
		Playlists []qqListItem `json:"v_list"`
		Total     int
		Finish    bool `json:"bFinish"`
	}
	params := map[string]any{"uin": grant.EncryptedUser, "offset": remaining, "size": PageSize}
	if err := a.rpc(ctx, grant, "music.musicasset.PlaylistFavRead", "CgiGetPlaylistFavInfo", params, &saved); err != nil {
		return PersonalLibrary{}, err
	}
	ownedCount := len(out)
	for _, item := range saved.Playlists {
		add(item)
	}
	if remaining > 0 {
		out = out[ownedCount:]
		first = 0
	}
	if first > len(out) {
		first = len(out)
	}
	last := min(first+PageSize, len(out))
	return PersonalLibrary{Playlists: out[first:last], More: last < len(out) || remaining+len(saved.Playlists) < saved.Total || (!saved.Finish && len(saved.Playlists) == PageSize)}, nil
}

type qqLibrarySong struct {
	MID, Name, Title string
	Singer           []artist
	Album            struct{ Name string }
}

func (a qqAccount) Tracks(ctx context.Context, secret, id string, page int) (Results, error) {
	grant, err := decodeAccountSecret[qqGrant](secret)
	if err != nil {
		return Results{}, err
	}
	offset := (page - 1) * PageSize
	playlistID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Results{}, err
	}
	var payload struct {
		Songs    []json.RawMessage `json:"songlist"`
		Total    int
		HasMore  int `json:"hasmore"`
		Playlist struct {
			Count int `json:"songnum"`
		} `json:"dirinfo"`
	}
	params := map[string]any{"disstid": playlistID, "tag": true, "song_begin": offset, "song_num": PageSize, "userinfo": true, "orderlist": true, "enc_host_uin": grant.EncryptedUser}
	if err := a.rpc(ctx, grant, "music.srfDissInfo.DissInfo", "CgiGetDiss", params, &payload); err != nil {
		return Results{}, err
	}
	out := Results{Tracks: []Track{}, More: payload.HasMore > 0 || max(payload.Total, payload.Playlist.Count) > offset+len(payload.Songs) || len(payload.Songs) == PageSize}
	for _, raw := range payload.Songs {
		var row struct {
			qqLibrarySong
			Track qqLibrarySong `json:"songInfo"`
			Info  qqLibrarySong `json:"track_info"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			continue
		}
		song := row.qqLibrarySong
		if song.MID == "" {
			song = row.Track
		}
		if song.MID == "" {
			song = row.Info
		}
		title := song.Name
		if title == "" {
			title = song.Title
		}
		out.add("tencent", song.MID, title, artistNames(song.Singer), song.Album.Name)
	}
	return out, nil
}

func (a qqAccount) Play(ctx context.Context, secret, id string) (*Media, error) {
	grant, err := decodeAccountSecret[qqGrant](secret)
	if err != nil {
		return nil, err
	}
	var body struct {
		Prefixes []string                      `json:"sip"`
		URLs     []struct{ Purl, Vkey string } `json:"midurlinfo"`
	}
	song, err := a.tencentSongDetails(ctx, id)
	mediaID := song.File.MediaMid
	if err != nil || !ValidID("tencent", mediaID) {
		mediaID = id
	}
	args := map[string]any{"filename": []string{"M500" + id + mediaID + ".mp3"}, "guid": strconv.FormatInt(time.Now().UnixMilli()%10000000000, 10),
		"songmid": []string{id}, "songtype": []int{song.Type}, "uin": grant.user(), "ctx": 0}
	if err := a.rpc(ctx, grant, "music.vkey.GetVkey", "UrlGetVkey", args, &body); err != nil {
		return nil, err
	}
	for _, item := range body.URLs {
		if item.Purl == "" || (item.Vkey == "" && !strings.Contains(item.Purl, "vkey=")) {
			continue
		}
		for _, prefix := range append(body.Prefixes, "https://dl.stream.qqmusic.qq.com/") {
			if media, _ := parseMedia("tencent", strings.TrimRight(prefix, "/")+"/"+strings.TrimLeft(item.Purl, "/"), nil); media != nil {
				return media, nil
			}
		}
	}
	return nil, nil
}
