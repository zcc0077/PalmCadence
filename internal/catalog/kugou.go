package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c Catalog) kugouSearch(ctx context.Context, query string, page int) (Results, error) {
	var payload struct {
		Status int
		Data   struct {
			Total int
			Info  []struct {
				Hash, Songname, Filename, Singername string
				Album                                string `json:"album_name"`
			}
		}
	}
	args := url.Values{"keyword": {query}, "page": {strconv.Itoa(page)}, "pagesize": {strconv.Itoa(PageSize)}}
	// The legacy public catalog accepts HTTP and carries no session or credentials.
	err := c.get(ctx, "http://mobilecdn.kugou.com/api/v3/search/song", args, "", "IPhone-8990-searchSong", &payload)
	if err != nil {
		return Results{}, err
	}
	if payload.Status != 1 {
		return Results{}, fmt.Errorf("Kugou search status %d", payload.Status)
	}
	result := Results{More: payload.Data.Total > page*PageSize}
	for _, row := range payload.Data.Info {
		artist, name, _ := strings.Cut(row.Filename, " - ")
		if row.Singername != "" {
			artist = row.Singername
		}
		if row.Songname != "" {
			name = row.Songname
		}
		result.add("kugou", row.Hash, name, artist, row.Album)
	}
	return result, nil
}

func (c Catalog) kugouMedia(ctx context.Context, id string) (string, error) {
	var payload struct {
		Status int
		URL    string
	}
	args := url.Values{"cmd": {"playInfo"}, "hash": {id}}
	err := c.get(ctx, "https://m.kugou.com/app/i/getSongInfo.php", args, "https://m.kugou.com/", "Mozilla/5.0", &payload)
	if err != nil || payload.Status != 1 {
		return "", err
	}
	return payload.URL, nil
}
