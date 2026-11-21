package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	qqReferer = "https://y.qq.com/"
	qqAgent   = "Mozilla/5.0"
)

func (c Catalog) tencentSearch(ctx context.Context, query string, page int) (Results, error) {
	var payload struct {
		Code, Subcode int
		Data          struct {
			Song *struct {
				TotalNum int
				List     []struct {
					SongMid, SongName, AlbumName string
					Singer                       []artist
				}
			}
		}
	}
	args := url.Values{"format": {"json"}, "p": {strconv.Itoa(page)}, "n": {strconv.Itoa(PageSize)}, "w": {query}}
	err := c.get(ctx, "https://c.y.qq.com/soso/fcgi-bin/search_for_qq_cp", args, qqReferer, qqAgent, &payload)
	if err != nil {
		return Results{}, err
	}
	if payload.Code != 0 || payload.Subcode != 0 || payload.Data.Song == nil {
		return Results{}, fmt.Errorf("QQ search code %d/%d", payload.Code, payload.Subcode)
	}
	result := Results{More: payload.Data.Song.TotalNum > page*PageSize}
	for _, song := range payload.Data.Song.List {
		result.add("tencent", song.SongMid, song.SongName, artistNames(song.Singer), song.AlbumName)
	}
	return result, nil
}

type tencentSong struct {
	Mid  string
	Type int
	File struct {
		MediaMid string `json:"media_mid"`
		SizeMP3  int64  `json:"size_128mp3"`
		SizeAAC  int64  `json:"size_96aac"`
	}
}

func (c Catalog) tencentSongDetails(ctx context.Context, id string) (tencentSong, error) {
	var response struct{ Data []tencentSong }
	err := c.get(ctx, "https://c.y.qq.com/v8/fcg-bin/fcg_play_single_song.fcg",
		url.Values{"songmid": {id}, "platform": {"yqq"}, "format": {"json"}}, qqReferer, qqAgent, &response)
	if err != nil || len(response.Data) == 0 {
		return tencentSong{}, err
	}
	if response.Data[0].Mid != id {
		return tencentSong{}, nil
	}
	return response.Data[0], nil
}

// Request the site's normal guest playback URLs, without an account session.
func (c Catalog) tencentMedia(ctx context.Context, id string) (string, error) {
	song, err := c.tencentSongDetails(ctx, id)
	if err != nil || !ValidID("tencent", song.File.MediaMid) {
		return "", err
	}
	var filenames []string
	if song.File.SizeMP3 > 0 {
		filenames = append(filenames, "M500"+song.File.MediaMid+".mp3")
	}
	if song.File.SizeAAC > 0 {
		filenames = append(filenames, "C400"+song.File.MediaMid+".m4a")
	}
	if len(filenames) == 0 {
		return "", nil
	}
	mids := make([]string, len(filenames))
	types := make([]int, len(filenames))
	for i := range mids {
		mids[i], types[i] = id, song.Type
	}
	request, err := json.Marshal(map[string]any{
		"req_0": map[string]any{
			"module": "vkey.GetVkeyServer", "method": "CgiGetVkey",
			"param": map[string]any{
				"guid":    strconv.FormatInt(time.Now().UnixMilli()%10000000000, 10),
				"songmid": mids, "songtype": types, "filename": filenames, "uin": "0", "loginflag": 1, "platform": "20",
			},
		},
	})
	if err != nil {
		return "", err
	}
	var payload struct {
		Response struct {
			Data struct {
				SIP  []string
				URLs []struct{ Purl, Vkey string } `json:"midurlinfo"`
			}
		} `json:"req_0"`
	}
	err = c.get(ctx, "https://u.y.qq.com/cgi-bin/musicu.fcg",
		url.Values{"data": {string(request)}, "format": {"json"}, "platform": {"yqq.json"}}, qqReferer, qqAgent, &payload)
	if err != nil {
		return "", err
	}
	for _, result := range payload.Response.Data.URLs {
		if result.Purl == "" || result.Vkey == "" {
			continue
		}
		for _, prefix := range payload.Response.Data.SIP {
			stream, _ := url.Parse(strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(result.Purl, "/"))
			if MediaURL("tencent", stream) {
				return stream.String(), nil
			}
		}
	}
	return "", nil
}
