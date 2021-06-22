package scrobbler

import (
	"context"
	"sort"
	"time"

	"github.com/navidrome/navidrome/log"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/utils/singleton"
)

const nowPlayingExpire = 60 * time.Minute

type NowPlayingInfo struct {
	TrackID    string
	Start      time.Time
	Username   string
	PlayerId   string
	PlayerName string
}

type Broker interface {
	NowPlaying(ctx context.Context, playerId string, playerName string, trackId string) error
	GetNowPlaying(ctx context.Context) ([]NowPlayingInfo, error)
	Submit(ctx context.Context, playerId int, trackId string, playTime time.Time) error
}

type broker struct {
	ds      model.DataStore
	playMap *ttlcache.Cache
}

func GetBroker(ds model.DataStore) Broker {
	instance := singleton.Get(broker{}, func() interface{} {
		m := ttlcache.NewCache()
		m.SkipTTLExtensionOnHit(true)
		_ = m.SetTTL(nowPlayingExpire)
		return &broker{ds: ds, playMap: m}
	})
	return instance.(*broker)
}

func (s *broker) NowPlaying(ctx context.Context, playerId string, playerName string, trackId string) error {
	username, _ := request.UsernameFrom(ctx)
	info := NowPlayingInfo{
		TrackID:    trackId,
		Start:      time.Now(),
		Username:   username,
		PlayerId:   playerId,
		PlayerName: playerName,
	}
	_ = s.playMap.Set(playerId, info)
	s.dispatchNowPlaying(ctx, trackId)
	return nil
}

func (s *broker) dispatchNowPlaying(ctx context.Context, trackId string) {
	t, err := s.ds.MediaFile(ctx).Get(trackId)
	if err != nil {
		log.Error(ctx, "Error retrieving mediaFile", "id", trackId, err)
		return
	}
	// TODO Parallelize
	for name, constructor := range scrobblers {
		log.Debug(ctx, "Sending NowPlaying info", "scrobbler", name, "track", t.Title, "artist", t.Artist)
		err := func() error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			s := constructor(s.ds)
			return s.NowPlaying(ctx, t)
		}()
		if err != nil {
			log.Error(ctx, "Error sending NowPlayingInfo", "scrobbler", name, "track", t.Title, "artist", t.Artist, err)
			return
		}
	}
}

func (s *broker) GetNowPlaying(ctx context.Context) ([]NowPlayingInfo, error) {
	var res []NowPlayingInfo
	for _, playerId := range s.playMap.GetKeys() {
		value, err := s.playMap.Get(playerId)
		if err != nil {
			continue
		}
		info := value.(NowPlayingInfo)
		res = append(res, info)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Start.After(res[j].Start)
	})
	return res, nil
}

func (s *broker) Submit(ctx context.Context, playerId int, trackId string, playTime time.Time) error {
	panic("implement me")
}

var scrobblers map[string]Constructor

func Register(name string, init Constructor) {
	if scrobblers == nil {
		scrobblers = make(map[string]Constructor)
	}
	scrobblers[name] = init
}
