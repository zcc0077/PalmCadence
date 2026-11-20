package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zcc0077/PalmCadence/internal/catalog"
)

type stream struct {
	media    *catalog.Media
	source   string
	id       string
	region   string
	cacheKey string
	expires  time.Time
}

type appServer struct {
	catalog   catalog.Catalog
	media     *http.Client
	cache     *audioCache
	web       http.Handler
	host      string
	localhost string
	mu        sync.Mutex
	entries   map[string]stream
}

var audioType = map[string]string{"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac"}
var singleRange = regexp.MustCompile(`^bytes=(\d{1,12}-\d{0,12}|-\d{1,12})$`)

func respond(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"message": message})
}

func (s *appServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if req.Host != s.host && req.Host != s.localhost {
		fail(w, http.StatusForbidden, "仅允许从本机访问。")
		return
	}
	switch origin := req.Header.Get("Origin"); origin {
	case "", "http://" + s.host, "http://" + s.localhost:
	case "null", "file://":
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	default:
		fail(w, http.StatusForbidden, "仅允许从本机访问。")
		return
	}

	path := req.URL.Path
	isMedia := strings.HasPrefix(path, "/media/")
	if req.Method != http.MethodGet && !(req.Method == http.MethodHead && isMedia) &&
		!(req.Method == http.MethodPost && path == "/api/cache") {
		fail(w, http.StatusMethodNotAllowed, "请求方式不受支持。")
		return
	}
	if isMedia {
		s.sendMedia(w, req, strings.TrimPrefix(path, "/media/"))
		return
	}
	switch path {
	case "/", "/player.css", "/player.js", "/icon.png", "/itunes-badge.gif":
		if s.web != nil {
			s.web.ServeHTTP(w, req)
			return
		}
	case "/api/health":
		respond(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case "/api/search", "/api/play":
		s.serveCatalog(w, req)
		return
	case "/api/cache":
		s.serveCache(w, req)
		return
	}
	fail(w, http.StatusNotFound, "找不到这个地址。")
}

func (s *appServer) serveCache(w http.ResponseWriter, req *http.Request) {
	if s.cache == nil {
		fail(w, http.StatusServiceUnavailable, "本机暂时无法写入音频缓存。")
		return
	}
	if req.Method == http.MethodPost {
		args := req.URL.Query()
		limit, err := strconv.Atoi(args.Get("limit"))
		clear := args.Get("clear") == "1"
		if !(clear && len(args) == 1) && !(len(args) == 1 && err == nil && validCacheLimit(limit)) {
			fail(w, http.StatusBadRequest, "请选择一个缓存上限。")
			return
		}
		if err := s.cache.configure(limit, clear); err != nil {
			logFailure("cache", err)
			fail(w, http.StatusInternalServerError, "暂时无法修改音频缓存。")
			return
		}
	}
	respond(w, http.StatusOK, s.cache.status())
}

func (s *appServer) serveCatalog(w http.ResponseWriter, req *http.Request) {
	args := req.URL.Query()
	source := args.Get("source")
	if !catalog.Known(source) {
		fail(w, http.StatusBadRequest, "请选择一个音乐平台。")
		return
	}
	provider := s.catalog
	if source == "apple" {
		switch country := args.Get("country"); country {
		case "":
		case "US", "CN", "HK", "TW", "JP":
			provider.Storefront = country
		default:
			fail(w, http.StatusBadRequest, "请选择一个 Apple Music 地区。")
			return
		}
	}
	if req.URL.Path == "/api/search" {
		query := strings.TrimSpace(args.Get("q"))
		page := 1
		if raw := args.Get("page"); raw != "" {
			page, _ = strconv.Atoi(raw)
		}
		if !utf8.ValidString(query) || query == "" || utf8.RuneCountInString(query) > 80 || page < 1 || page > 25 {
			fail(w, http.StatusBadRequest, "请输入 1 到 80 个字符的歌名或歌手。")
			return
		}
		results, err := provider.Search(req.Context(), source, query, page)
		if err != nil {
			logFailure(source, err)
			fail(w, http.StatusBadGateway, "暂时无法连接这个平台，请稍后重试。")
			return
		}
		results.More = page < 25 && results.More
		respond(w, http.StatusOK, results)
		return
	}
	id := args.Get("id")
	if !catalog.ValidID(source, id) {
		fail(w, http.StatusBadRequest, "曲目编号无效。")
		return
	}
	region := ""
	if source == "apple" {
		region = provider.Storefront
	}
	entry := stream{source: source, id: id, region: region}
	if source != "apple" {
		entry.cacheKey = audioCacheKey(source, region, id)
	}
	extension := s.cache.extension(entry.cacheKey)
	if extension != "" {
		respond(w, http.StatusOK, map[string]string{"path": "/media/" + s.remember(entry, extension)})
		return
	}
	media, err := provider.Resolve(req.Context(), source, id)
	if err != nil {
		logFailure(source, err)
		fail(w, http.StatusBadGateway, "暂时无法连接这个平台，请稍后重试。")
		return
	}
	if media == nil {
		fail(w, http.StatusNotFound, "平台未提供可播音源。")
		return
	}
	entry.media = media
	key := s.remember(entry, "."+media.Extension)
	respond(w, http.StatusOK, map[string]string{"path": "/media/" + key})
}

func (s *appServer) remember(entry stream, extension string) string {
	var token [18]byte
	rand.Read(token[:])
	key := hex.EncodeToString(token[:]) + extension
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldest string
	for name, entry := range s.entries {
		if !entry.expires.After(now) {
			delete(s.entries, name)
			continue
		}
		if oldest == "" || entry.expires.Before(s.entries[oldest].expires) {
			oldest = name
		}
	}
	if len(s.entries) >= 128 {
		delete(s.entries, oldest)
	}
	entry.expires = now.Add(20 * time.Minute)
	s.entries[key] = entry
	return key
}

func (s *appServer) sendMedia(w http.ResponseWriter, req *http.Request, key string) {
	s.mu.Lock()
	entry, found := s.entries[key]
	s.mu.Unlock()
	if !found || !entry.expires.After(time.Now()) {
		fail(w, http.StatusNotFound, "播放地址已过期，请重新选择曲目。")
		return
	}
	byteRange := req.Header.Get("Range")
	if byteRange != "" && !singleRange.MatchString(byteRange) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if file, extension := s.cache.open(entry.cacheKey); file != nil {
		defer file.Close()
		w.Header().Set("Content-Type", audioType[extension])
		http.ServeContent(w, req, file.Name(), time.Time{}, file)
		return
	}
	media := entry.media
	if media == nil {
		provider := s.catalog
		if entry.region != "" {
			provider.Storefront = entry.region
		}
		var err error
		media, err = provider.Resolve(req.Context(), entry.source, entry.id)
		if err != nil || media == nil {
			fail(w, http.StatusNotFound, "缓存已移除，当前音源暂时不可播放。")
			return
		}
	}
	current := media.URL
	for range 4 {
		upstream, err := http.NewRequestWithContext(req.Context(), req.Method, current.String(), nil)
		if err != nil {
			break
		}
		upstream.Header.Set("Accept-Encoding", "identity")
		if byteRange != "" {
			upstream.Header.Set("Range", byteRange)
		}
		response, err := s.media.Do(upstream)
		if err != nil {
			logFailure(entry.source, err)
			break
		}
		switch response.StatusCode {
		case 301, 302, 303, 307, 308:
			next, err := response.Location()
			response.Body.Close()
			if err != nil || !catalog.MediaURL(entry.source, next) {
				fail(w, http.StatusBadGateway, "播放地址已失效。")
				return
			}
			current = next
		case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
			defer response.Body.Close()
			kind := strings.ToLower(response.Header.Get("Content-Type"))
			if response.StatusCode != http.StatusRequestedRangeNotSatisfiable &&
				(kind != "" && !strings.HasPrefix(kind, "audio/") && !strings.HasPrefix(kind, "application/octet-stream") && !strings.HasPrefix(kind, "video/mp4")) {
				fail(w, http.StatusBadGateway, "原平台暂时无法提供音频。")
				return
			}
			if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
				w.Header().Set("Content-Range", response.Header.Get("Content-Range"))
				w.WriteHeader(response.StatusCode)
				return
			}
			w.Header().Set("Content-Type", audioType[media.Extension])
			for _, name := range []string{"Content-Range", "Accept-Ranges", "Content-Length"} {
				if value := response.Header.Get(name); value != "" && response.Header.Get("Content-Encoding") == "" {
					w.Header().Set(name, value)
				}
			}
			w.WriteHeader(response.StatusCode)
			if req.Method != http.MethodHead {
				s.cache.copyTo(w, response.Body, entry.cacheKey, media.Extension, completeAudioLength(response))
			}
			return
		default:
			response.Body.Close()
			fail(w, http.StatusBadGateway, "原平台暂时无法提供音频。")
			return
		}
	}
	fail(w, http.StatusBadGateway, "原平台暂时无法提供音频。")
}

func completeAudioLength(response *http.Response) int64 {
	size := response.ContentLength
	if size <= 0 || response.Header.Get("Content-Encoding") != "" {
		return 0
	}
	if response.StatusCode == http.StatusOK ||
		response.Header.Get("Content-Range") == fmt.Sprintf("bytes 0-%d/%d", size-1, size) {
		return size
	}
	return 0
}
