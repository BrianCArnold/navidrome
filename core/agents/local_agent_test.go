package agents

import (
	"context"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	. "github.com/navidrome/navidrome/utils/gg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("localAgent", func() {
	var ds model.DataStore
	var ctx context.Context
	var agent Interface

	BeforeEach(func() {
		ds = &tests.MockDataStore{}
		ctx = context.Background()
		agent = localsConstructor(ds)
	})

	Describe("GetSongLyrics", func() {
		It("should parse LRC file", func() {
			lyricFetcher, _ := agent.(LyricsRetriever)

			mf := model.MediaFile{
				Path: "tests/fixtures/01 Invisible (RED) Edit Version.mp3",
			}

			lyrics, err := lyricFetcher.GetSongLyrics(ctx, &mf)
			Expect(err).ToNot(HaveOccurred())
			Expect(lyrics).To(Equal(model.LyricList{
				{
					DisplayArtist: "",
					DisplayTitle:  "",
					Lang:          "xxx",
					Line: []model.Line{
						{Start: P(int64(0)), Value: "Line 1"},
						{Start: P(int64(5210)), Value: "Line 2"},
						{Start: P(int64(12450)), Value: "Line 3"},
					},
					Offset: nil,
					Synced: true,
				},
			}))
		})

		It("should parse both LRC and TXT", func() {
			lyricFetcher, _ := agent.(LyricsRetriever)

			mf := model.MediaFile{
				Path: "tests/fixtures/test.wav",
			}

			lyrics, err := lyricFetcher.GetSongLyrics(ctx, &mf)
			Expect(err).ToNot(HaveOccurred())
			Expect(lyrics).To(Equal(model.LyricList{
				{
					DisplayArtist: "Artist",
					DisplayTitle:  "Title",
					Lang:          "xxx",
					Line: []model.Line{
						{Start: P(int64(0)), Value: "Line 1"},
						{Start: P(int64(5210)), Value: "Line 2"},
						{Start: P(int64(12450)), Value: "Line 5"},
					},
					Offset: P(int64(100)),
					Synced: true,
				},
				{

					DisplayArtist: "",
					DisplayTitle:  "",
					Lang:          "xxx",
					Line: []model.Line{
						{
							Start: nil,
							Value: "Unsynchronized lyric line 1",
						},
						{
							Start: nil,
							Value: "Unsynchronized lyric line 2",
						},
						{
							Start: nil,
							Value: "Unsynchronized lyric line 3",
						},
					},
					Offset: nil,
					Synced: false,
				},
			}))
		})
	})
})
