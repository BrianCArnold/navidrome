package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/model"
)

const LocalAgentName = "local"

var (
	supportedExtensions = []string{"lrc", "txt"}
)

type localAgent struct {
	ds model.DataStore
}

func localsConstructor(ds model.DataStore) Interface {
	return &localAgent{ds}
}

func (p *localAgent) AgentName() string {
	return LocalAgentName
}

func (p *localAgent) GetArtistTopSongs(ctx context.Context, id, artistName, mbid string, count int) ([]Song, error) {
	top, err := p.ds.MediaFile(ctx).GetAll(model.QueryOptions{
		Sort:  "playCount",
		Order: "desc",
		Max:   count,
		Filters: squirrel.And{
			squirrel.Eq{"artist_id": id},
			squirrel.Or{
				squirrel.Eq{"starred": true},
				squirrel.Eq{"rating": 5},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var result []Song
	for _, s := range top {
		result = append(result, Song{
			Name: s.Title,
			MBID: s.MbzReleaseTrackID,
		})
	}
	return result, nil
}

func (p *localAgent) GetSongLyrics(ctx context.Context, mf *model.MediaFile) (model.LyricList, error) {
	lyrics := model.LyricList{}
	extension := filepath.Ext(mf.Path)
	basePath := mf.Path[0 : len(mf.Path)-len(extension)]

	for _, ext := range supportedExtensions {
		lrcPath := fmt.Sprintf("%s.%s", basePath, ext)
		contents, err := os.ReadFile(lrcPath)

		if err != nil {
			continue
		}

		lyric, err := model.ToLyrics("xxx", string(contents))
		if err != nil {
			return nil, err
		}

		lyrics = append(lyrics, *lyric)
	}

	return lyrics, nil
}

func init() {
	Register(LocalAgentName, localsConstructor)
}

var _ ArtistTopSongsRetriever = (*localAgent)(nil)
var _ LyricsRetriever = (*localAgent)(nil)
