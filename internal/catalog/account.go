package catalog

import (
	"context"
	"encoding/json"
	"strconv"
)

var ErrAccountExpired = ErrNetEaseSession

type AccountID string

func (id *AccountID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*id = AccountID(value)
		return nil
	}
	*id = AccountID(data)
	return nil
}

type AccountProfile struct {
	ID   AccountID `json:"userId"`
	Name string    `json:"nickname"`
}

type PersonalPlaylist struct {
	ID    AccountID `json:"id"`
	Name  string    `json:"name"`
	Count int       `json:"trackCount"`
}

type PersonalLibrary struct {
	Playlists []PersonalPlaylist `json:"playlist"`
	More      bool               `json:"more"`
}

type LoginChallenge struct {
	Key, State, Link, Image string
}

type AccountProvider interface {
	Start(context.Context) (LoginChallenge, error)
	Poll(context.Context, LoginChallenge) (int, string, error)
	Profile(context.Context, string) (AccountProfile, error)
	Playlists(context.Context, string, AccountID, int) (PersonalLibrary, error)
	Tracks(context.Context, string, string, int) (Results, error)
	Play(context.Context, string, string) (*Media, error)
}

func (c Catalog) Account(source string) AccountProvider {
	switch source {
	case "netease":
		return neteaseAccount{c}
	case "tencent":
		return qqAccount{c}
	case "kugou":
		return kugouAccount{c}
	case "kuwo":
		return kuwoAccount{c}
	default:
		return nil
	}
}

func ValidAccountSecret(source, value string) bool {
	if source == "netease" {
		return ValidNetEaseLogin(value)
	}
	return value != "" && len(value) <= 6144 && json.Valid([]byte(value))
}

func ValidPlaylistID(source, id string) bool {
	if source == "kugou" || source == "tencent" || source == "kuwo" || source == "netease" {
		return numericID.MatchString(id)
	}
	return false
}

func decodeAccountSecret[T any](secret string) (T, error) {
	var result T
	if err := json.Unmarshal([]byte(secret), &result); err != nil {
		return result, ErrAccountExpired
	}
	return result, nil
}

func encodeAccountSecret(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

type neteaseAccount struct{ Catalog }

func (a neteaseAccount) Start(ctx context.Context) (LoginChallenge, error) {
	key, err := a.NetEaseQRStart(ctx)
	return LoginChallenge{Key: key, Link: "https://music.163.com/login?codekey=" + key}, err
}
func (a neteaseAccount) Poll(ctx context.Context, flow LoginChallenge) (int, string, error) {
	return a.NetEaseQRPoll(ctx, flow.Key)
}
func (a neteaseAccount) Profile(ctx context.Context, secret string) (AccountProfile, error) {
	profile, err := a.NetEaseAccount(ctx, secret)
	return AccountProfile{ID: AccountID(strconv.FormatInt(profile.ID, 10)), Name: profile.Name}, err
}
func (a neteaseAccount) Playlists(ctx context.Context, secret string, uid AccountID, page int) (PersonalLibrary, error) {
	user, err := strconv.ParseInt(string(uid), 10, 64)
	if err != nil {
		return PersonalLibrary{}, ErrAccountExpired
	}
	library, err := a.NetEasePlaylists(ctx, secret, user, page)
	result := PersonalLibrary{Playlists: []PersonalPlaylist{}, More: library.More}
	for _, item := range library.Playlists {
		result.Playlists = append(result.Playlists, PersonalPlaylist{AccountID(strconv.FormatInt(item.ID, 10)), item.Name, item.Count})
	}
	return result, err
}
func (a neteaseAccount) Tracks(ctx context.Context, secret, id string, page int) (Results, error) {
	return a.NetEasePlaylistTracks(ctx, secret, id, page)
}
func (a neteaseAccount) Play(ctx context.Context, secret, id string) (*Media, error) {
	return a.NetEaseAuthorizedMedia(ctx, secret, id)
}

func takePlaylists(all []PersonalPlaylist, page int) PersonalLibrary {
	first := (page - 1) * PageSize
	if first >= len(all) {
		return PersonalLibrary{Playlists: []PersonalPlaylist{}}
	}
	end := min(first+PageSize, len(all))
	return PersonalLibrary{Playlists: all[first:end], More: end < len(all)}
}
