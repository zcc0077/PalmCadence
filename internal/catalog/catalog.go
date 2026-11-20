package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const PageSize = 18

type Track struct {
	Source  string `json:"source"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Album   string `json:"album"`
	URL     string `json:"url"`
	Preview bool   `json:"preview,omitempty"`
}

type Results struct {
	Tracks []Track `json:"tracks"`
	More   bool    `json:"more"`
}

type Media struct {
	URL       *url.URL
	Extension string
}

type Catalog struct {
	Client     *http.Client
	Storefront string
}

var (
	numericID  = regexp.MustCompile(`^[0-9]{1,18}$`)
	tencentID  = regexp.MustCompile(`^[A-Za-z0-9]{1,24}$`)
	kugouID    = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
	idPatterns = map[string]*regexp.Regexp{
		"netease": numericID, "tencent": tencentID, "kuwo": numericID, "kugou": kugouID, "apple": numericID,
	}
	trackPages = map[string]string{
		"netease": "https://music.163.com/song?id=",
		"tencent": "https://y.qq.com/n/ryqq/songDetail/",
		"kuwo":    "https://www.kuwo.cn/play_detail/",
		"kugou":   "https://www.kugou.com/song/#hash=",
	}
	mediaDomains = map[string][]string{
		"netease": {"music.126.net", "music.163.com"},
		"tencent": {"music.qq.com", "qqmusic.qq.com", "y.qq.com"},
		"kuwo":    {"kuwo.cn", "kwcdn.com", "kwcdn.net"},
		"kugou":   {"kugou.com"},
		"apple":   {"mzstatic.com", "itunes.apple.com"},
	}
)

func Known(source string) bool {
	return idPatterns[source] != nil
}

func ValidID(source, id string) bool {
	return Known(source) && idPatterns[source].MatchString(id)
}

func MediaURL(source string, u *url.URL) bool {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Opaque != "" || u.Port() != "" || u.User != nil {
		return false
	}
	if source == "apple" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range mediaDomains[source] {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (c Catalog) Search(ctx context.Context, source, query string, page int) (Results, error) {
	var result Results
	var err error
	switch source {
	case "netease":
		result, err = c.neteaseSearch(ctx, query, page)
	case "tencent":
		result, err = c.tencentSearch(ctx, query, page)
	case "kuwo":
		result, err = c.kuwoSearch(ctx, query, page)
	case "kugou":
		result, err = c.kugouSearch(ctx, query, page)
	case "apple":
		result, err = c.appleSearch(ctx, query, page)
	default:
		return result, fmt.Errorf("unknown catalog %q", source)
	}
	if result.Tracks == nil {
		result.Tracks = []Track{}
	}
	return result, err
}

func (c Catalog) Resolve(ctx context.Context, source, id string) (*Media, error) {
	var raw string
	var err error
	switch source {
	case "netease":
		raw, err = c.neteaseMedia(ctx, id, "")
	case "tencent":
		raw, err = c.tencentMedia(ctx, id)
	case "kuwo":
		raw, err = c.kuwoMedia(ctx, id)
	case "kugou":
		raw, err = c.kugouMedia(ctx, id)
	case "apple":
		raw, err = c.appleMedia(ctx, id)
	}
	return parseMedia(source, raw, err)
}

func parseMedia(source, raw string, err error) (*Media, error) {
	if err != nil || raw == "" {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil || !MediaURL(source, u) {
		return nil, nil
	}
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
	if source == "apple" {
		extension = "m4a"
	}
	switch extension {
	case "mp3", "m4a", "aac":
		return &Media{URL: u, Extension: extension}, nil
	default:
		return nil, nil
	}
}

func clean(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}

func (r *Results) add(source, id, title, artist, album string) {
	if ValidID(source, id) && clean(title) != "" {
		r.Tracks = append(r.Tracks, Track{
			Source: source, ID: id, Title: clean(title), Artist: clean(artist), Album: clean(album),
			URL: trackPages[source] + id,
		})
	}
}

type artist struct {
	Name string
}

func artistNames(artists []artist) string {
	names := make([]string, 0, len(artists))
	for _, a := range artists {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return strings.Join(names, " / ")
}

func (c Catalog) request(ctx context.Context, endpoint string, args url.Values, body, contentType, referer, agent string) ([]byte, error) {
	if len(args) != 0 {
		endpoint += "?" + args.Encode()
	}
	method := http.MethodGet
	var payload io.Reader
	if contentType != "" {
		method = http.MethodPost
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if agent == "" {
		agent = "PalmCadence/0.2"
	}
	req.Header.Set("User-Agent", agent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.readCatalog(req)
}

func (c Catalog) readCatalog(req *http.Request) ([]byte, error) {
	data, _, err := c.readCatalogResponse(req)
	return data, err
}

func (c Catalog) readCatalogResponse(req *http.Request) ([]byte, []*http.Cookie, error) {
	res, err := c.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%s returned HTTP %d", req.URL.Host, res.StatusCode)
	}
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if len(data) > limit {
		return nil, nil, fmt.Errorf("response from %s exceeded limit", req.URL.Host)
	}
	return data, res.Cookies(), err
}

func (c Catalog) get(ctx context.Context, endpoint string, args url.Values, referer, agent string, dst any) error {
	data, err := c.request(ctx, endpoint, args, "", "", referer, agent)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}
