package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/titanous/json5"
)

func (c Catalog) kuwoSearch(ctx context.Context, query string, page int) (Results, error) {
	args := url.Values{
		"all": {query}, "ft": {"music"}, "itemset": {"web_2013"}, "client": {"kt"},
		"pn": {strconv.Itoa(page - 1)}, "rn": {strconv.Itoa(PageSize)}, "rformat": {"json"}, "encoding": {"utf8"},
	}
	data, err := c.request(ctx, "https://search.kuwo.cn/r.s", args, "", "", "", "")
	if err != nil {
		return Results{}, err
	}
	var payload struct {
		Total   string
		Abslist []struct {
			MusicRid, SongName, Name, Artist, Album string
		}
	}
	if err := json5.Unmarshal(data, &payload); err != nil {
		return Results{}, err
	}
	total, err := strconv.Atoi(payload.Total)
	if err != nil {
		return Results{}, fmt.Errorf("invalid Kuwo result count")
	}
	result := Results{More: total > page*PageSize}
	for _, song := range payload.Abslist {
		title := song.SongName
		if title == "" {
			title = song.Name
		}
		result.add("kuwo", strings.TrimPrefix(song.MusicRid, "MUSIC_"), title, song.Artist, song.Album)
	}
	return result, nil
}

const kuwoHome = "https://www.kuwo.cn/"
const kuwoAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36"

func (c Catalog) kuwoMedia(ctx context.Context, id string) (string, error) {
	return c.kuwoMediaWithAccount(ctx, id, kuwoGrant{})
}

func (c Catalog) kuwoMediaWithAccount(ctx context.Context, id string, account kuwoGrant) (string, error) {
	// The public web player issues a fresh guest cookie even without an account.
	cookie, err := c.kuwoGuest(ctx)
	if err != nil {
		return "", err
	}
	salt, err := rand.Int(rand.Reader, big.NewInt(90_000_000))
	if err != nil {
		return "", err
	}
	var requestID [16]byte
	rand.Read(requestID[:])
	requestID[6] = (requestID[6] & 0x0f) | 0x40
	requestID[8] = (requestID[8] & 0x3f) | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", requestID[:4], requestID[4:6], requestID[6:8], requestID[8:10], requestID[10:])
	args := url.Values{"mid": {id}, "type": {"music"}, "httpsStatus": {"1"}, "reqId": {uuid}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kuwoHome+"api/v1/www/music/playUrl?"+args.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", kuwoAgent)
	req.Header.Set("Referer", kuwoHome)
	req.Header.Set("Secret", kuwoGuestProof(cookie.Value, 10_000_000+salt.Int64()))
	req.AddCookie(cookie)
	for name, value := range account.Cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	body, err := c.readCatalog(req)
	if err != nil {
		return "", err
	}
	var payload struct {
		Code int
		Data struct{ URL string }
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.Code != 200 {
		return "", nil
	}
	return payload.Data.URL, nil
}

func (c Catalog) kuwoGuest(ctx context.Context) (*http.Cookie, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kuwoHome, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", kuwoAgent)
	res, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 512<<10))
	for _, cookie := range res.Cookies() {
		if strings.HasPrefix(cookie.Name, "Hm_Iuvt_") && len(cookie.Value) > 0 && len(cookie.Value) <= 256 && cookie.Valid() == nil {
			return cookie, nil
		}
	}
	return nil, fmt.Errorf("Kuwo did not issue a guest session")
}

func kuwoGuestProof(cookie string, nonce int64) string {
	const modulus = 2147483647
	const multiplier = 9253
	state := int64(59910100)
	masked := []byte(cookie)
	for i, ch := range masked {
		state = (multiplier*state + 23) % modulus
		masked[i] = ch ^ byte(255*state/modulus)
	}
	return fmt.Sprintf("%x%08x", masked, nonce)
}
