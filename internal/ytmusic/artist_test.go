package ytmusic

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("invalid test json: %v", err)
	}
	return v
}

func TestArtistBrowseIDFromURL(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantID string
		wantOK bool
	}{
		{name: "bare channel id", input: "UC_x5XG1OV2P6uZZ5FSM9Ttw", wantID: "UC_x5XG1OV2P6uZZ5FSM9Ttw", wantOK: true},
		{name: "yt music channel url", input: "https://music.youtube.com/channel/UCkWQ0gDrqOCarmUKmppD7GQ?feature=share", wantID: "UCkWQ0gDrqOCarmUKmppD7GQ", wantOK: true},
		{name: "youtube channel url", input: "https://www.youtube.com/channel/UCkWQ0gDrqOCarmUKmppD7GQ/releases", wantID: "UCkWQ0gDrqOCarmUKmppD7GQ", wantOK: true},
		{name: "playlist url is not artist", input: "https://music.youtube.com/playlist?list=OLAK5uy_abc", wantOK: false},
		{name: "handle url needs resolve", input: "https://music.youtube.com/@daftpunk", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := artistBrowseIDFromURL(tt.input)
			if ok != tt.wantOK || got != tt.wantID {
				t.Fatalf("artistBrowseIDFromURL(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestParseArtistSearch(t *testing.T) {
	payload := mustJSON(t, `{
		"contents": {
			"tabbedSearchResultsRenderer": {
				"tabs": [{
					"tabRenderer": {
						"content": {
							"sectionListRenderer": {
								"contents": [
									{
										"musicCardShelfRenderer": {
											"title": {"runs": [{"text": "Daft Punk"}]},
											"subtitle": {"runs": [{"text": "Artist"}, {"text": " • "}, {"text": "4.2M subscribers"}]},
											"onTap": {"browseEndpoint": {"browseId": "UC_artist_card"}},
											"thumbnail": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [
												{"url": "https://lh3.example.com/small.jpg"},
												{"url": "https://lh3.example.com/large.jpg"}
											]}}}
										}
									},
									{
										"musicShelfRenderer": {
											"contents": [
												{
													"musicResponsiveListItemRenderer": {
														"navigationEndpoint": {
															"browseEndpoint": {
																"browseId": "UC_artist_row",
																"browseEndpointContextSupportedConfigs": {
																	"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_ARTIST"}
																}
															}
														},
														"thumbnail": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [{"url": "https://lh3.example.com/row.jpg"}]}}},
														"flexColumns": [
															{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Daft Punk Tribute"}]}}},
															{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Artist • 12K subscribers"}]}}}
														]
													}
												},
												{
													"musicResponsiveListItemRenderer": {
														"navigationEndpoint": {
															"browseEndpoint": {
																"browseId": "MPREb_not_artist",
																"browseEndpointContextSupportedConfigs": {
																	"browseEndpointContextMusicConfig": {"pageType": "MUSIC_PAGE_TYPE_ALBUM"}
																}
															}
														},
														"flexColumns": [
															{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Discovery"}]}}}
														]
													}
												}
											]
										}
									}
								]
							}
						}
					}
				}]
			}
		}
	}`)

	got := parseArtistSearch(payload)
	if len(got) != 2 {
		t.Fatalf("len(parseArtistSearch) = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "UC_artist_card" || got[0].Name != "Daft Punk" || got[0].Cover != "https://lh3.example.com/large.jpg" || got[0].Subscribers != "4.2M subscribers" {
		t.Fatalf("first artist mismatch: %+v", got[0])
	}
	if got[1].ID != "UC_artist_row" || got[1].Name != "Daft Punk Tribute" || got[1].URL != "https://music.youtube.com/channel/UC_artist_row" {
		t.Fatalf("second artist mismatch: %+v", got[1])
	}
}

func TestParseArtistPageAndMoreEndpoints(t *testing.T) {
	payload := mustJSON(t, `{
		"header": {
			"musicImmersiveHeaderRenderer": {
				"title": {"runs": [{"text": "Daft Punk"}]},
				"thumbnail": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [{"url": "https://lh3.example.com/artist.jpg"}]}}}
			}
		},
		"contents": {
			"singleColumnBrowseResultsRenderer": {
				"tabs": [{
					"tabRenderer": {
						"content": {
							"sectionListRenderer": {
								"contents": [
									{
										"musicCarouselShelfRenderer": {
											"header": {
												"musicCarouselShelfBasicHeaderRenderer": {
													"title": {"runs": [{"text": "Albums"}]},
													"moreContentButton": {
														"buttonRenderer": {
															"navigationEndpoint": {
																"browseEndpoint": {"browseId": "MPADUC123", "params": "albums_params"}
															}
														}
													}
												}
											},
											"contents": [{
												"musicTwoRowItemRenderer": {
													"title": {"runs": [{"text": "Random Access Memories"}]},
													"subtitle": {"runs": [{"text": "Album"}, {"text": " • "}, {"text": "2013"}]},
													"navigationEndpoint": {"browseEndpoint": {"browseId": "MPREb_ram"}},
													"thumbnailRenderer": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [{"url": "https://lh3.example.com/ram.jpg"}]}}},
													"thumbnailOverlay": {
														"musicItemThumbnailOverlayRenderer": {
															"content": {
																"musicPlayButtonRenderer": {
																	"playNavigationEndpoint": {
																		"watchPlaylistEndpoint": {"playlistId": "OLAK5uy_ram"}
																	}
																}
															}
														}
													}
												}
											}]
										}
									},
									{
										"musicCarouselShelfRenderer": {
											"header": {
												"musicCarouselShelfBasicHeaderRenderer": {
													"title": {"runs": [{"text": "Singles & EPs"}]}
												}
											},
											"contents": [{
												"musicTwoRowItemRenderer": {
													"title": {"runs": [{"text": "Get Lucky"}]},
													"subtitle": {"runs": [{"text": "Single"}, {"text": " • "}, {"text": "2013"}]},
													"navigationEndpoint": {"browseEndpoint": {"browseId": "MPREb_lucky"}},
													"thumbnailRenderer": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [{"url": "https://lh3.example.com/lucky.jpg"}]}}}
												}
											}]
										}
									}
								]
							}
						}
					}
				}]
			}
		}
	}`)

	page, more := parseArtistPage(payload, "UC123")
	if page.Name != "Daft Punk" || page.Cover != "https://lh3.example.com/artist.jpg" {
		t.Fatalf("artist header mismatch: %+v", page)
	}
	if len(page.Albums) != 1 || page.Albums[0].Title != "Random Access Memories" || page.Albums[0].Year != "2013" || page.Albums[0].URL != "https://music.youtube.com/playlist?list=OLAK5uy_ram" {
		t.Fatalf("albums mismatch: %+v", page.Albums)
	}
	if len(page.Singles) != 1 || page.Singles[0].Type != "single" || page.Singles[0].URL != "https://music.youtube.com/browse/MPREb_lucky" {
		t.Fatalf("singles mismatch: %+v", page.Singles)
	}
	if more["album"].browseID != "MPADUC123" || more["album"].params != "albums_params" {
		t.Fatalf("more endpoints mismatch: %+v", more)
	}
}

func TestHiResCoverURL(t *testing.T) {
	in := "https://lh3.googleusercontent.com/abc=w120-h120-l90-rj"
	want := "https://lh3.googleusercontent.com/abc=w600-h600-l90-rj"
	if got := hiResCoverURL(in); got != want {
		t.Fatalf("hiResCoverURL(%q) = %q, want %q", in, got, want)
	}
	in2 := "https://yt3.ggpht.com/xyz=s120-c-k-c0x00ffffff-no-rj"
	want2 := "https://yt3.ggpht.com/xyz=s600-c-k-c0x00ffffff-no-rj"
	if got := hiResCoverURL(in2); got != want2 {
		t.Fatalf("hiResCoverURL(%q) = %q, want %q", in2, got, want2)
	}
}
