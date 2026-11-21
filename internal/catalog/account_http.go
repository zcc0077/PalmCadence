package catalog

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const authWebAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36"

var (
	wechatTicket = regexp.MustCompile(`(?:/connect/qrcode/|uuid=)([A-Za-z0-9_-]{8,160})`)
	wechatStatus = regexp.MustCompile(`wx_errcode\s*=\s*(\d+);(?:\s*window\.wx_code\s*=\s*'([^']*)')?`)
	authKey      = regexp.MustCompile(`^[a-zA-Z0-9_-]{4,256}$`)
)

func randomID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (c Catalog) authCatalog() Catalog {
	if c.AccountClient != nil {
		c.Client = c.AccountClient
	}
	return c
}

func (c Catalog) startWeChat(ctx context.Context, appID, callback string) (LoginChallenge, error) {
	state := randomID()
	args := url.Values{"appid": {appID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {"snsapi_login"}, "state": {state}}
	endpoint := "https://open.weixin.qq.com/connect/qrconnect"
	page, err := c.authCatalog().request(ctx, endpoint, args, "", "", "", authWebAgent)
	if err != nil {
		return LoginChallenge{}, err
	}
	match := wechatTicket.FindSubmatch(page)
	if match == nil {
		return LoginChallenge{}, fmt.Errorf("WeChat did not issue a QR ticket")
	}
	key := string(match[1])
	data, err := c.authCatalog().request(ctx, "https://open.weixin.qq.com/connect/qrcode/"+key, nil, "", "", endpoint, authWebAgent)
	if err != nil {
		return LoginChallenge{}, err
	}
	kind := http.DetectContentType(data)
	if (kind != "image/png" && kind != "image/jpeg") || len(data) > 128<<10 {
		return LoginChallenge{}, fmt.Errorf("WeChat returned an invalid QR image")
	}
	return LoginChallenge{Key: key, State: state, Image: "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(data)}, nil
}

func (c Catalog) pollWeChat(ctx context.Context, ticket string) (int, string, error) {
	if !authKey.MatchString(ticket) {
		return 800, "", nil
	}
	args := url.Values{"uuid": {ticket}, "_": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
	body, err := c.authCatalog().request(ctx, "https://lp.open.weixin.qq.com/connect/l/qrconnect", args, "", "", "https://open.weixin.qq.com/", authWebAgent)
	if err != nil {
		if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() && ctx.Err() == nil {
			return 801, "", nil
		}
		return 0, "", err
	}
	match := wechatStatus.FindSubmatch(body)
	if match == nil {
		return 0, "", fmt.Errorf("WeChat returned no QR status")
	}
	switch string(match[1]) {
	case "408":
		return 801, "", nil
	case "404":
		return 802, "", nil
	case "405":
		code := strings.TrimSpace(string(match[2]))
		if !authKey.MatchString(code) {
			return 0, "", fmt.Errorf("WeChat returned no authorization code")
		}
		return 803, code, nil
	case "402":
		return 800, "", nil
	case "403":
		return 403, "", nil
	default:
		return 0, "", fmt.Errorf("unsupported WeChat QR status %s", match[1])
	}
}
