package catalog

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// NetEase's guest request envelope is adapted from Meting, under the MIT license.
func (c Catalog) netease(ctx context.Context, route string, payload any, dst any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	message := string(body)
	digest := md5.Sum([]byte("nobody" + route + "use" + message + "md5forencrypt"))
	clear := []byte(fmt.Sprintf("%s-36cd479b6b5-%s-36cd479b6b5-%x", route, message, digest))
	cipher, err := aes.NewCipher([]byte("e82ckenh8dichen8"))
	if err != nil {
		return err
	}
	size := cipher.BlockSize()
	padding := size - len(clear)%size
	for range padding {
		clear = append(clear, byte(padding))
	}
	for offset := 0; offset < len(clear); offset += size {
		cipher.Encrypt(clear[offset:], clear[offset:])
	}
	form := url.Values{"params": {strings.ToUpper(hex.EncodeToString(clear))}}
	data, err := c.request(ctx, "https://music.163.com/eapi/"+strings.TrimPrefix(route, "/api/"), nil,
		form.Encode(), "application/x-www-form-urlencoded", "https://music.163.com/", "NeteaseMusic/8.7.01")
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func (c Catalog) neteaseSearch(ctx context.Context, query string, page int) (Results, error) {
	var payload struct {
		Code   int
		Result struct {
			SongCount int
			Songs     []struct {
				ID   int64
				Name string
				Al   struct{ Name string }
				Ar   []artist
			}
		}
	}
	err := c.netease(ctx, "/api/cloudsearch/pc", map[string]any{
		"s": query, "type": 1, "limit": PageSize, "total": "true", "offset": (page - 1) * PageSize,
	}, &payload)
	if err != nil {
		return Results{}, err
	}
	if payload.Code != 200 {
		return Results{}, fmt.Errorf("NetEase search code %d", payload.Code)
	}
	result := Results{More: payload.Result.SongCount > page*PageSize}
	for _, song := range payload.Result.Songs {
		result.add("netease", strconv.FormatInt(song.ID, 10), song.Name, artistNames(song.Ar), song.Al.Name)
	}
	return result, nil
}

func (c Catalog) neteaseMedia(ctx context.Context, id string) (string, error) {
	var payload struct {
		Code int
		Data []struct {
			URL           string
			FreeTrialInfo *struct{}
		}
	}
	err := c.netease(ctx, "/api/song/enhance/player/url", map[string]any{"ids": []string{id}, "br": 128000}, &payload)
	if err != nil || len(payload.Data) == 0 || payload.Code != 200 {
		return "", err
	}
	// Present only a full guest stream here. Apple has a separate preview screen.
	if payload.Data[0].FreeTrialInfo != nil {
		return "", nil
	}
	return payload.Data[0].URL, nil
}
