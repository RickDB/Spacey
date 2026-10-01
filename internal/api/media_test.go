package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"spacey/internal/model"
	"testing"
)

func TestProviderIDsMatchWatchedMoviesAndEpisodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/movie":
			_, _ = w.Write([]byte(`[{"title":"Arrival","path":"/radarr/Arrival (2016)","tmdbId":329865,"movieFile":{"path":"/radarr/Arrival (2016)/Arrival.mkv"}}]`))
		case "/api/v3/series":
			_, _ = w.Write([]byte(`[{"id":7,"title":"Silo","path":"/sonarr/Silo","tvdbId":403245}]`))
		case "/api/v3/episode":
			_, _ = w.Write([]byte(`[{"id":1,"seriesId":7,"episodeFileId":42,"seasonNumber":1,"episodeNumber":2},{"id":2,"seriesId":7,"episodeFileId":42,"seasonNumber":1,"episodeNumber":3}]`))
		case "/api/v3/episodefile":
			_, _ = w.Write([]byte(`[{"id":42,"seriesId":7,"path":"/sonarr/Silo/S01E02-E03.mkv"}]`))
		case "/sync/watched":
			if r.URL.Query().Get("apikey") != "test" {
				t.Error("missing MDBList key")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"movies": []any{map[string]any{"movie": map[string]any{"ids": map[string]any{"tmdb": 329865}}}}, "episodes": []any{map[string]any{"show": map[string]any{"ids": map[string]any{"tvdb": 403245}}, "episode": map[string]any{"season": 1, "number": 2}}}, "pagination": map[string]any{"next_cursor": ""}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newMediaState()
	m.mdblistBase = server.URL
	m.mdblistKey = "test"
	m.radarr = mediaService{URL: server.URL, Key: "test", RemoteRoot: "/radarr", CanonicalRoot: "/media/Movies"}
	m.sonarr = mediaService{URL: server.URL, Key: "test", RemoteRoot: "/sonarr", CanonicalRoot: "/media/TV"}
	if e := m.syncCatalog(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := m.syncWatched(context.Background()); e != nil {
		t.Fatal(e)
	}
	movie, status := m.resolve(model.Node{CanonicalPath: "/media/Movies/Arrival (2016)/Arrival.mkv", Name: "Arrival.mkv", Extension: ".mkv", Type: "file"})
	if status != "watched" || movie.TMDB != "329865" || movie.MatchSource != "Radarr" {
		t.Fatalf("movie=%+v status=%s", movie, status)
	}
	episode, status := m.resolve(model.Node{CanonicalPath: "/media/TV/Silo/S01E02-E03.mkv", Name: "S01E02-E03.mkv", Extension: ".mkv", Type: "file"})
	if status != "unwatched" || episode.MatchSource != "Sonarr" {
		t.Fatalf("episode=%+v status=%s", episode, status)
	}
	unknown, status := m.resolve(model.Node{CanonicalPath: "/media/Other/Arrival.mkv", Name: "Arrival.mkv", Extension: ".mkv", Type: "file"})
	if status != "unknown" || unknown.MatchSource != "" {
		t.Fatalf("unknown=%+v status=%s", unknown, status)
	}
}
func TestSiloExactPathMatchesWatchedMovie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/catalog":
			_, _ = w.Write([]byte(`{"items":[{"content_id":"movie:heat-1995","type":"movie","title":"Heat"}],"page":{"has_more":false}}`))
		case "/api/v2/catalog/items/movie:heat-1995":
			_, _ = w.Write([]byte(`{"content_id":"movie:heat-1995","type":"movie","title":"Heat","tmdb_id":"949","folder_paths":["/silo/Heat (1995)"],"versions":[{"file_path":"/silo/Heat (1995)/Heat.mkv"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newMediaState()
	m.silo = mediaService{URL: server.URL, Key: "test", RemoteRoot: "/silo", CanonicalRoot: "/media/Movies"}
	if e := m.syncCatalog(context.Background()); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.watched = map[string]bool{"movie:tmdb:949": true}
	m.watchedAt = m.catalogAt
	m.mu.Unlock()
	v, status := m.resolve(model.Node{CanonicalPath: "/media/Movies/Heat (1995)/Heat.mkv", Name: "Heat.mkv", Extension: ".mkv", Type: "file"})
	if status != "watched" || v.MatchSource != "Silo" {
		t.Fatalf("media=%+v status=%s", v, status)
	}
}

func TestMDBListWatchedCursorAndSeason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"movies":[{"movie":{"ids":{"imdb":"tt123"}}}],"pagination":{"next_cursor":"next"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"seasons":[{"show":{"ids":{"tvdb":12345}},"season":{"number":2}}],"pagination":{"next_cursor":""}}`))
	}))
	defer server.Close()
	m := newMediaState()
	m.mdblistBase = server.URL
	m.mdblistKey = "test"
	if e := m.syncWatched(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !m.watched["movie:imdb:tt123"] || !m.watched["season:tvdb:12345:s02"] {
		t.Fatalf("keys=%v", m.watched)
	}
	m.byPath["/media/show.mkv"] = mediaInfo{Kind: "episode", TVDB: "12345", Season: 2, Episode: 5, MatchSource: "Sonarr"}
	_, status := m.resolve(model.Node{CanonicalPath: "/media/show.mkv"})
	if status != "watched" {
		t.Fatalf("season episode status=%s", status)
	}
}
