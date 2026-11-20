package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zcc0077/PalmCadence/internal/catalog"
)

func testCache(t *testing.T) *audioCache {
	t.Helper()
	cache, err := openAudioCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestCacheQuotaAndClear(t *testing.T) {
	cache := testCache(t)
	keys := []string{
		audioCacheKey("netease", "", "1"),
		audioCacheKey("netease", "", "2"),
		audioCacheKey("netease", "", "3"),
	}
	for index, key := range keys {
		name := key + ".mp3"
		file, err := os.Create(filepath.Join(cache.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		size := 12 * mib
		if err := file.Truncate(size); err != nil {
			t.Fatal(err)
		}
		file.Close()
		cache.files[key] = cachedFile{name, size, time.Unix(int64(index), 0)}
		cache.bytes += size
	}
	file, _ := cache.open(keys[0])
	if file == nil {
		t.Fatal("missing cache file")
	}
	defer file.Close()
	if err := cache.configure(32, false); err != nil {
		t.Fatal(err)
	}
	if cache.extension(keys[1]) != "" || cache.extension(keys[0]) != ".mp3" || cache.status().Bytes != 24*mib {
		t.Fatalf("expected least-recently-used file to be removed: %+v", cache.status())
	}

	pendingKey := audioCacheKey("netease", "", "4")
	pending := cache.begin(pendingKey, "mp3", 3)
	if pending == nil {
		t.Fatal("failed to reserve cache space")
	}
	pending.Write([]byte("abc"))
	if err := cache.configure(0, true); err != nil {
		t.Fatal(err)
	}
	pending.finish(nil)
	if got := cache.status(); got.Limit != 32 || got.Count != 0 || got.Bytes != 0 {
		t.Fatalf("clear retained audio or changed the limit: %+v", got)
	}
	if cache.reserved != 0 {
		t.Fatalf("clear retained reservations: %d", cache.reserved)
	}
	if _, err := file.Read(make([]byte, 1)); err != nil {
		t.Fatalf("clear interrupted an open stream: %v", err)
	}
	if err := cache.configure(0, false); err != nil {
		t.Fatal(err)
	}
	reopened, err := openAudioCache(cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.status(); got.Limit != 0 || got.Count != 0 || got.Bytes != 0 || reopened.begin(pendingKey, "mp3", 1) != nil {
		t.Fatalf("cache disable setting was lost: %+v", got)
	}
}

type mediaTransport func(*http.Request) (*http.Response, error)

func (f mediaTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestMediaCacheReusesCompleteRangedResponse(t *testing.T) {
	cache := testCache(t)
	upstream, err := url.Parse("https://music.126.net/audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	entry := stream{source: "netease", id: "123", cacheKey: audioCacheKey("netease", "", "123"),
		media: &catalog.Media{URL: upstream, Extension: "mp3"}}
	calls := 0
	app := &appServer{
		cache: cache, entries: make(map[string]stream), host: "localhost",
		media: &http.Client{Transport: mediaTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			header := make(http.Header)
			header.Set("Content-Type", "audio/mpeg")
			switch req.Header.Get("Range") {
			case "bytes=0-1":
				header.Set("Content-Range", "bytes 0-1/5")
				return &http.Response{StatusCode: 206, ContentLength: 2, Header: header, Body: io.NopCloser(strings.NewReader("he"))}, nil
			case "bytes=0-":
				header.Set("Content-Range", "bytes 0-4/5")
				return &http.Response{StatusCode: 206, ContentLength: 5, Header: header, Body: io.NopCloser(strings.NewReader("hello"))}, nil
			default:
				return nil, errors.New("unavailable")
			}
		})},
	}
	fetch := func(path, byteRange string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+path, nil)
		req.Header.Set("Range", byteRange)
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		return res
	}
	mediaPath := "/media/" + app.remember(entry, ".mp3")
	if res := fetch(mediaPath, "bytes=0-1"); res.Code != 206 || res.Body.String() != "he" || cache.status().Count != 0 {
		t.Fatalf("partial audio was cached: %d %q %+v", res.Code, res.Body, cache.status())
	}
	if res := fetch(mediaPath, "bytes=0-"); res.Code != 206 || res.Body.String() != "hello" || cache.status().Bytes != 5 {
		t.Fatalf("complete audio was not cached: %d %q %+v", res.Code, res.Body, cache.status())
	}
	res := fetch("/api/play?source=netease&id=123", "")
	if res.Code != 200 {
		t.Fatalf("cache did not bypass catalog resolution: %d %s", res.Code, res.Body)
	}
	if res := fetch(mediaPath, "bytes=2-3"); res.Code != 206 || res.Body.String() != "ll" || res.Header().Get("Content-Range") != "bytes 2-3/5" || calls != 2 {
		t.Fatalf("expected ranged cache hit: %d %q %v (%d upstream calls)", res.Code, res.Body, res.Header(), calls)
	}
}

func TestCacheRejectsIncompleteWrite(t *testing.T) {
	cache := testCache(t)
	key := audioCacheKey("apple", "US", "3")
	cache.copyTo(io.Discard, strings.NewReader("abc"), key, "m4a", 4)
	if got := cache.status(); got.Count != 0 || cache.reserved != 0 {
		t.Fatalf("short audio remained cached: %+v", got)
	}
}
