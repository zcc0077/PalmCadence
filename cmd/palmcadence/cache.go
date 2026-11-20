package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	mib         int64 = 1024 * 1024
	diskReserve       = 128 * mib
)

var cacheFilename = regexp.MustCompile(`^([0-9a-f]{64})\.(mp3|m4a|aac)$`)

type cacheStatus struct {
	Limit int   `json:"limit"`
	Bytes int64 `json:"bytes"`
	Count int   `json:"count"`
	Free  int64 `json:"free"`
}

type cachedFile struct {
	name string
	size int64
	used time.Time
}

type audioCache struct {
	dir      string
	mu       sync.Mutex
	limit    int
	bytes    int64
	reserved int64
	files    map[string]cachedFile
	writes   map[string]*cacheWrite
}

func validCacheLimit(limit int) bool {
	switch limit {
	case 0, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192:
		return true
	}
	return false
}

func openAudioCache(dir string) (*audioCache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	cache := &audioCache{dir: dir, limit: 32, files: make(map[string]cachedFile), writes: make(map[string]*cacheWrite)}
	if data, err := os.ReadFile(filepath.Join(dir, "limit")); err == nil {
		if limit, err := strconv.Atoi(string(data)); err == nil && validCacheLimit(limit) {
			cache.limit = limit
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".part" && entry.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
			continue
		}
		match := cacheFilename.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if existing, ok := cache.files[match[1]]; ok {
			if err := os.Remove(filepath.Join(dir, existing.name)); err != nil {
				return nil, err
			}
			cache.bytes -= existing.size
		}
		cache.files[match[1]] = cachedFile{entry.Name(), info.Size(), info.ModTime()}
		cache.bytes += info.Size()
	}
	if err := cache.trim(int64(cache.limit) * mib); err != nil {
		return nil, err
	}
	return cache, nil
}

func audioCacheKey(source, region, id string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\x00"+region+"\x00"+id)))
}

func (c *audioCache) status() cacheStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cacheStatus{c.limit, c.bytes, len(c.files), availableDisk(c.dir)}
}

func availableDisk(dir string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

// trim runs under the cache lock and removes the least recently used files.
func (c *audioCache) trim(target int64) error {
	for c.bytes > target || (target == 0 && len(c.files) > 0) {
		var oldest string
		for key, file := range c.files {
			if oldest == "" || file.used.Before(c.files[oldest].used) {
				oldest = key
			}
		}
		file := c.files[oldest]
		if err := os.Remove(filepath.Join(c.dir, file.name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(c.files, oldest)
		c.bytes -= file.size
	}
	return nil
}

func (c *audioCache) configure(limit int, clear bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pending := range c.writes {
		pending.discard()
	}
	if clear {
		return c.trim(0)
	}
	if err := c.trim(int64(limit) * mib); err != nil {
		return err
	}
	temp := filepath.Join(c.dir, "limit.part")
	if err := os.WriteFile(temp, []byte(strconv.Itoa(limit)), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, filepath.Join(c.dir, "limit")); err != nil {
		_ = os.Remove(temp)
		return err
	}
	c.limit = limit
	return nil
}

func (c *audioCache) extension(key string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return filepath.Ext(c.files[key].name)
}

func (c *audioCache) open(key string) (*os.File, string) {
	if c == nil {
		return nil, ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.files[key]
	if !ok {
		return nil, ""
	}
	path := filepath.Join(c.dir, entry.name)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			delete(c.files, key)
			c.bytes -= entry.size
		}
		return nil, ""
	}
	entry.used = time.Now()
	c.files[key] = entry
	_ = os.Chtimes(path, entry.used, entry.used)
	return file, filepath.Ext(entry.name)[1:]
}

type cacheWrite struct {
	cache         *audioCache
	file          *os.File
	key, name     string
	size, written int64
}

func (c *audioCache) begin(key, extension string, size int64) *cacheWrite {
	if c == nil || key == "" || size <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := int64(c.limit)*mib - c.reserved - size
	if remaining < 0 || c.files[key].name != "" || c.writes[key] != nil {
		return nil
	}
	free := availableDisk(c.dir)
	if free < 0 {
		return nil
	}
	remaining = min(remaining, c.bytes+free-diskReserve-c.reserved-size)
	if remaining < 0 {
		return nil
	}
	if err := c.trim(remaining); err != nil {
		return nil
	}
	name := key + "." + extension
	file, err := os.OpenFile(filepath.Join(c.dir, name+".part"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	pending := &cacheWrite{cache: c, file: file, key: key, name: name, size: size}
	c.reserved += size
	c.writes[key] = pending
	return pending
}

// Write keeps cache errors from interrupting playback.
func (p *cacheWrite) Write(data []byte) (int, error) {
	c := p.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.file != nil {
		if int64(len(data)) <= p.size-p.written {
			if n, err := p.file.Write(data); err == nil {
				p.written += int64(n)
				return len(data), nil
			}
		}
		p.discard()
	}
	return len(data), nil
}

func (p *cacheWrite) discard() {
	if p.file == nil {
		return
	}
	_ = p.file.Close()
	_ = os.Remove(p.file.Name())
	delete(p.cache.writes, p.key)
	p.cache.reserved -= p.size
	p.file = nil
}

func (p *cacheWrite) finish(streamErr error) {
	c := p.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.file == nil {
		return
	}
	if streamErr != nil || p.written != p.size {
		p.discard()
		return
	}
	file := p.file
	closeErr := file.Close()
	p.file = nil
	delete(c.writes, p.key)
	c.reserved -= p.size
	if closeErr != nil || os.Rename(file.Name(), filepath.Join(c.dir, p.name)) != nil {
		_ = os.Remove(file.Name())
		return
	}
	c.files[p.key] = cachedFile{p.name, p.size, time.Now()}
	c.bytes += p.size
}

func (c *audioCache) copyTo(dst io.Writer, src io.Reader, key, extension string, size int64) {
	if pending := c.begin(key, extension, size); pending != nil {
		_, err := io.Copy(dst, io.TeeReader(src, pending))
		pending.finish(err)
		return
	}
	_, _ = io.Copy(dst, src)
}
