package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var ErrNetEaseSession = errors.New("NetEase session no longer authorized")
var neteaseQRKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,256}$`)

type NetEaseProfile struct {
	ID   int64  `json:"userId"`
	Name string `json:"nickname"`
}

type NetEasePlaylist struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"trackCount"`
}

type NetEaseLibrary struct {
	Playlists []NetEasePlaylist `json:"playlist"`
	More      bool              `json:"more"`
}

func (c Catalog) NetEaseQRStart(ctx context.Context) (string, error) {
	var result struct {
		Code int
		Key  string `json:"unikey"`
	}
	err := c.netease(ctx, "/api/login/qrcode/unikey", map[string]int{"type": 3}, &result)
	if err != nil {
		return "", err
	}
	if result.Code != 200 || !neteaseQRKey.MatchString(result.Key) {
		return "", fmt.Errorf("NetEase QR key code %d", result.Code)
	}
	return result.Key, nil
}

func ValidNetEaseLogin(value string) bool {
	cookie := &http.Cookie{Name: "MUSIC_U", Value: value}
	return value != "" && len(value) <= 2048 && !strings.ContainsAny(value, " ,") && cookie.Valid() == nil
}

func (c Catalog) NetEaseQRPoll(ctx context.Context, key string) (int, string, error) {
	var result struct{ Code int }
	cookies, err := c.neteaseRequest(ctx, "/api/login/qrcode/client/login", map[string]any{"type": 3, "key": key}, "", &result)
	if err != nil {
		return 0, "", err
	}
	if result.Code == 803 {
		for _, cookie := range cookies {
			if cookie.Name == "MUSIC_U" && ValidNetEaseLogin(cookie.Value) && cookie.MaxAge >= 0 {
				return result.Code, cookie.Value, nil
			}
		}
		return 0, "", errors.New("NetEase QR confirmation missing session cookie")
	}
	return result.Code, "", nil
}

func netEaseAccountCode(code int) error {
	switch code {
	case 200:
		return nil
	case 301, 401:
		return ErrNetEaseSession
	default:
		return fmt.Errorf("NetEase account code %d", code)
	}
}

func (c Catalog) NetEaseAccount(ctx context.Context, login string) (NetEaseProfile, error) {
	var result struct {
		Code    int
		Profile NetEaseProfile
	}
	_, err := c.neteaseRequest(ctx, "/api/nuser/account/get", map[string]string{}, login, &result)
	if err != nil {
		return NetEaseProfile{}, err
	}
	if err := netEaseAccountCode(result.Code); err != nil {
		return NetEaseProfile{}, err
	}
	result.Profile.Name = clean(result.Profile.Name)
	if result.Profile.ID <= 0 {
		return NetEaseProfile{}, ErrNetEaseSession
	}
	return result.Profile, nil
}

func (c Catalog) NetEasePlaylists(ctx context.Context, login string, userID int64, page int) (NetEaseLibrary, error) {
	var result struct {
		Code int
		NetEaseLibrary
	}
	_, err := c.neteaseRequest(ctx, "/api/user/playlist", map[string]any{
		"uid": userID, "offset": (page - 1) * PageSize, "limit": PageSize,
	}, login, &result)
	if err != nil {
		return NetEaseLibrary{}, err
	}
	if err := netEaseAccountCode(result.Code); err != nil {
		return NetEaseLibrary{}, err
	}
	library := NetEaseLibrary{Playlists: []NetEasePlaylist{}, More: result.More}
	for _, item := range result.Playlists {
		item.Name = clean(item.Name)
		if item.ID > 0 && item.Name != "" {
			library.Playlists = append(library.Playlists, item)
		}
	}
	return library, nil
}

func (c Catalog) NetEasePlaylistTracks(ctx context.Context, login, id string, page int) (Results, error) {
	var data struct {
		Code     int
		Playlist *struct {
			TrackIDs []struct {
				ID int64 `json:"id"`
			} `json:"trackIds"`
		}
	}
	_, err := c.neteaseRequest(ctx, "/api/v6/playlist/detail", map[string]any{"id": id, "n": 1, "s": 0}, login, &data)
	if err != nil {
		return Results{}, err
	}
	if err := netEaseAccountCode(data.Code); err != nil {
		return Results{}, err
	}
	if data.Playlist == nil {
		return Results{}, errors.New("NetEase playlist unavailable")
	}
	result := Results{Tracks: []Track{}}
	ids := data.Playlist.TrackIDs
	first := (page - 1) * PageSize
	if first >= len(ids) {
		return result, nil
	}
	last := min(first+PageSize, len(ids))
	batch, err := json.Marshal(ids[first:last])
	if err != nil {
		return Results{}, err
	}
	var detail struct {
		Code  int
		Songs []struct {
			ID   int64
			Name string
			Al   struct{ Name string }
			Ar   []artist
		}
	}
	_, err = c.neteaseRequest(ctx, "/api/v3/song/detail", map[string]string{"c": string(batch)}, login, &detail)
	if err != nil {
		return Results{}, err
	}
	if err := netEaseAccountCode(detail.Code); err != nil {
		return Results{}, err
	}
	ordered := make(map[int64]Track)
	for _, song := range detail.Songs {
		trackID := strconv.FormatInt(song.ID, 10)
		if ValidID("netease", trackID) && clean(song.Name) != "" {
			ordered[song.ID] = Track{Source: "netease", ID: trackID, Title: clean(song.Name),
				Artist: artistNames(song.Ar), Album: clean(song.Al.Name), URL: trackPages["netease"] + trackID}
		}
	}
	for _, track := range ids[first:last] {
		if item, ok := ordered[track.ID]; ok {
			result.Tracks = append(result.Tracks, item)
		}
	}
	result.More = last < len(ids)
	return result, nil
}

func (c Catalog) NetEaseAuthorizedMedia(ctx context.Context, login, id string) (*Media, error) {
	raw, err := c.neteaseMedia(ctx, id, login)
	return parseMedia("netease", raw, err)
}
