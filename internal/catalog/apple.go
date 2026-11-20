package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

type appleSong struct {
	TrackID        int64
	TrackName      string
	ArtistName     string
	CollectionName string
	TrackViewURL   string
	PreviewURL     string
}

func (c Catalog) appleResults(ctx context.Context, endpoint string, args url.Values) ([]appleSong, error) {
	args.Set("country", c.Storefront)
	args.Set("entity", "song")
	var result struct{ Results []appleSong }
	err := c.get(ctx, "https://itunes.apple.com/"+endpoint, args, "", "", &result)
	if err == nil && result.Results == nil {
		err = fmt.Errorf("Apple returned no results field")
	}
	return result.Results, err
}

func (c Catalog) appleSearch(ctx context.Context, query string, page int) (Results, error) {
	result := Results{}
	rows, err := c.appleResults(ctx, "search", url.Values{"term": {query}, "media": {"music"}, "limit": {"60"}})
	if err != nil {
		return result, err
	}
	var tracks []Track
	for _, row := range rows {
		id := strconv.FormatInt(row.TrackID, 10)
		link, err := url.Parse(row.TrackViewURL)
		if err != nil || row.TrackID <= 0 || clean(row.TrackName) == "" || link.Scheme != "https" || link.User != nil || link.Port() != "" ||
			(link.Hostname() != "music.apple.com" && link.Hostname() != "itunes.apple.com") {
			continue
		}
		preview, _ := url.Parse(row.PreviewURL)
		tracks = append(tracks, Track{
			Source: "apple", ID: id, Title: clean(row.TrackName), Artist: clean(row.ArtistName), Album: clean(row.CollectionName),
			URL: link.String(), Preview: MediaURL("apple", preview),
		})
	}
	first := (page - 1) * PageSize
	if first < len(tracks) {
		last := min(first+PageSize, len(tracks))
		result.Tracks = tracks[first:last]
		result.More = last < len(tracks)
	}
	return result, nil
}

func (c Catalog) appleMedia(ctx context.Context, id string) (string, error) {
	rows, err := c.appleResults(ctx, "lookup", url.Values{"id": {id}})
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if strconv.FormatInt(row.TrackID, 10) == id {
			return row.PreviewURL, nil
		}
	}
	return "", nil
}
