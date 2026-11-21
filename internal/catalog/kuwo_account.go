package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const kuwoWechatID = "wx41c1275bb3e28427"
const kuwoAuthURL = "https://i.kuwo.cn/US/platform/WeixinCallback.jsp"
const kuwoRedirect = "https://www.kuwo.cn/callback"
const kuwoCloud = "https://nplserver.kuwo.cn/pl.svc"

type kuwoAccount struct{ Catalog }

type kuwoGrant struct {
	User     AccountID
	SID      string
	Nickname string
	Device   string
	Cookies  map[string]string
}

func kuwoCallback() string {
	return kuwoAuthURL + "?" + url.Values{"f": {"web"}, "callbackUrl": {kuwoRedirect}}.Encode()
}

func (a kuwoAccount) Start(ctx context.Context) (LoginChallenge, error) {
	return a.startWeChat(ctx, kuwoWechatID, kuwoCallback())
}

func (a kuwoAccount) Poll(ctx context.Context, challenge LoginChallenge) (int, string, error) {
	code, authorization, err := a.pollWeChat(ctx, challenge.Key)
	if code != 803 || err != nil {
		return code, "", err
	}
	callback := kuwoCallback() + "&" + url.Values{"code": {authorization}, "state": {challenge.State}}.Encode()
	grant, err := a.exchange(ctx, callback, challenge.State)
	if err != nil {
		return 0, "", err
	}
	secret, err := encodeAccountSecret(grant)
	return 803, secret, err
}

func (a kuwoAccount) exchange(ctx context.Context, callback, device string) (kuwoGrant, error) {
	current, err := url.Parse(callback)
	if err != nil {
		return kuwoGrant{}, err
	}
	client := *a.authCatalog().Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cookies := make(map[string]string)
	for range 4 {
		if current.Scheme != "https" || (current.Host != "i.kuwo.cn" && current.Host != "www.kuwo.cn") || current.User != nil {
			return kuwoGrant{}, fmt.Errorf("Kuwo returned an unsupported authorization redirect")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return kuwoGrant{}, err
		}
		req.Header.Set("User-Agent", authWebAgent)
		req.Header.Set("Referer", kuwoHome)
		for name, value := range cookies {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
		resp, err := client.Do(req)
		if err != nil {
			// The callback URL contains an authorization code.
			return kuwoGrant{}, fmt.Errorf("Kuwo authorization callback unavailable")
		}
		for _, cookie := range resp.Cookies() {
			if cookie.Valid() == nil && len(cookie.Value) <= 1024 && cookie.Value != "" && cookie.MaxAge >= 0 {
				switch strings.ToLower(cookie.Name) {
				case "userid", "uid", "webuid", "sid", "websid", "sessionid", "uname", "nickname", "username", "kw_token":
					cookies[cookie.Name] = cookie.Value
				}
			}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		value := func(names ...string) string {
			for _, name := range names {
				for key, value := range cookies {
					if strings.EqualFold(name, key) {
						return value
					}
				}
			}
			return ""
		}
		id, sid := value("userid", "uid", "webuid"), value("sid", "websid", "sessionid")
		if numericID.MatchString(id) && sid != "" {
			return kuwoGrant{AccountID(id), sid, clean(value("nickname", "uname", "username")), device, cookies}, nil
		}
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			break
		}
		current, err = resp.Location()
		if err != nil {
			break
		}
	}
	return kuwoGrant{}, fmt.Errorf("Kuwo confirmation did not return an authenticated session")
}

func (a kuwoAccount) Profile(_ context.Context, secret string) (AccountProfile, error) {
	grant, err := decodeAccountSecret[kuwoGrant](secret)
	return AccountProfile{grant.User, grant.Nickname}, err
}

func (a kuwoAccount) private(ctx context.Context, endpoint string, args url.Values, grant kuwoGrant, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+args.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", authWebAgent)
	req.Header.Set("Referer", kuwoHome)
	for name, value := range grant.Cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	body, err := a.authCatalog().readCatalog(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

func kuwoAccountParams(grant kuwoGrant) url.Values {
	return url.Values{"uid": {string(grant.User)}, "sid": {grant.SID}, "encode": {"utf-8"}, "plat": {"ar"},
		"devid": {grant.Device}, "user": {grant.Device}, "prod": {"kwplayer_ar_12.2.2.0"}, "corp": {"kuwo"},
		"locationid": {"1"}, "ttime": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
}

func kuwoCount(raw AccountID) int { n, _ := strconv.Atoi(string(raw)); return max(0, n) }

func (a kuwoAccount) Playlists(ctx context.Context, secret string, _ AccountID, page int) (PersonalLibrary, error) {
	grant, err := decodeAccountSecret[kuwoGrant](secret)
	if err != nil {
		return PersonalLibrary{}, err
	}
	args := kuwoAccountParams(grant)
	args.Set("op", "pl3_getuserlists")
	args.Set("recommend", "1")
	var response struct {
		Code  *AccountID `json:"errcode"`
		UID   AccountID
		Items []struct {
			ID          AccountID
			Type, Title string
			MusicNum    AccountID
		} `json:"plist"`
	}
	if err := a.private(ctx, kuwoCloud, args, grant, &response); err != nil {
		return PersonalLibrary{}, err
	}
	if response.Code == nil || *response.Code != "0" || (response.UID != "" && response.UID != grant.User) {
		return PersonalLibrary{}, fmt.Errorf("Kuwo did not return the selected account's playlists")
	}
	items := make([]PersonalPlaylist, 0, len(response.Items))
	for _, item := range response.Items {
		if ValidPlaylistID("kuwo", string(item.ID)) && clean(item.Title) != "" && (item.Type == "GENERAL" || item.Type == "MYFAVORITE") {
			items = append(items, PersonalPlaylist{item.ID, clean(item.Title), kuwoCount(item.MusicNum)})
		}
	}
	return takePlaylists(items, page), nil
}

func (a kuwoAccount) Tracks(ctx context.Context, secret, id string, page int) (Results, error) {
	grant, err := decodeAccountSecret[kuwoGrant](secret)
	if err != nil {
		return Results{}, err
	}
	args := kuwoAccountParams(grant)
	args.Set("op", "pl3_getlist")
	args.Set("pid", id)
	args.Set("sig", "0")
	args.Set("pn", strconv.Itoa(page-1))
	var response struct {
		Code     *AccountID `json:"errcode"`
		Pages    AccountID  `json:"pagenum"`
		UID, PID AccountID
		Info     struct {
			Items []struct {
				ID                  AccountID
				Name, Artist, Album string
			} `json:"musiclist"`
		}
	}
	if err := a.private(ctx, kuwoCloud, args, grant, &response); err != nil {
		return Results{}, err
	}
	if response.Code == nil || *response.Code != "0" || (response.UID != "" && response.UID != grant.User) || (response.PID != "" && string(response.PID) != id) {
		return Results{}, fmt.Errorf("Kuwo did not return the selected playlist")
	}
	result := Results{Tracks: []Track{}, More: kuwoCount(response.Pages) > page}
	for _, item := range response.Info.Items {
		result.add("kuwo", string(item.ID), item.Name, item.Artist, item.Album)
	}
	return result, nil
}

func (a kuwoAccount) Play(ctx context.Context, secret, id string) (*Media, error) {
	grant, err := decodeAccountSecret[kuwoGrant](secret)
	if err != nil {
		return nil, err
	}
	raw, err := a.kuwoMediaWithAccount(ctx, id, grant)
	return parseMedia("kuwo", raw, err)
}
