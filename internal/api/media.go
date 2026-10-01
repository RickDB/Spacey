package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"spacey/internal/model"
	"strconv"
	"strings"
	"sync"
	"time"
)

type mediaService struct {
	URL           string `json:"url"`
	Key           string `json:"key"`
	RemoteRoot    string `json:"remote_root"`
	CanonicalRoot string `json:"canonical_root"`
}

func (s mediaService) configured() bool { return s.URL != "" && s.Key != "" }
func (s mediaService) canonical(path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	if s.RemoteRoot == "" || s.CanonicalRoot == "" {
		return path
	}
	from := filepath.Clean(s.RemoteRoot)
	to := filepath.Clean(s.CanonicalRoot)
	if path == from {
		return to
	}
	if strings.HasPrefix(path, from+string(os.PathSeparator)) {
		return filepath.Join(to, strings.TrimPrefix(path, from))
	}
	return ""
}

type mediaState struct {
	mu                         sync.RWMutex
	syncMu                     sync.Mutex
	client                     *http.Client
	mdblistKey, mdblistBase    string
	radarr, sonarr, silo       mediaService
	watched                    map[string]bool
	byPath                     map[string]mediaInfo
	byFolder                   map[string]mediaInfo
	watchedAt, catalogAt       time.Time
	watchedError, catalogError string
	sources                    map[string]int
}

func newMediaState() *mediaState {
	return &mediaState{client: &http.Client{Timeout: 30 * time.Second}, mdblistBase: "https://api.mdblist.com", watched: map[string]bool{}, byPath: map[string]mediaInfo{}, byFolder: map[string]mediaInfo{}, sources: map[string]int{}}
}
func (m *mediaState) applySettings(v appSettings) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.radarr = v.Radarr
	m.sonarr = v.Sonarr
	m.silo = v.Silo
	m.mdblistKey = v.MDBListKey
	m.byPath = map[string]mediaInfo{}
	m.byFolder = map[string]mediaInfo{}
	m.watched = map[string]bool{}
	m.catalogAt = time.Time{}
	m.watchedAt = time.Time{}
	m.catalogError = ""
	m.watchedError = ""
}
func (m *mediaState) configured() (bool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.radarr.configured() || m.sonarr.configured() || m.silo.configured(), m.mdblistKey != ""
}
func (m *mediaState) resolve(n model.Node) (mediaInfo, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	path := filepath.Clean(n.CanonicalPath)
	v, ok := m.byPath[path]
	if !ok {
		for folder := path; folder != ""; {
			if candidate, found := m.byFolder[folder]; found {
				v = candidate
				ok = true
				break
			}
			next := filepath.Dir(folder)
			if next == folder {
				break
			}
			folder = next
		}
	}
	if !ok {
		v = parseMedia(n)
		return v, "unknown"
	}
	if v.Kind == "show" && n.Type == "file" {
		parsed := parseMedia(n)
		if parsed.Kind == "episode" {
			v.Kind = "episode"
			v.Season = parsed.Season
			v.Episode = parsed.Episode
			v.episodeKeyGroups = [][]string{episodeKeys(v)}
		}
	}
	if m.watchedAt.IsZero() {
		return v, "unknown"
	}
	keys := mediaKeys(v)
	if len(keys) == 0 {
		return v, "unknown"
	}
	if len(v.episodeKeyGroups) > 0 {
		for _, group := range v.episodeKeyGroups {
			matched := false
			for _, key := range group {
				if m.watched[key] || m.watched[seasonFromEpisodeKey(key)] {
					matched = true
					break
				}
			}
			if !matched {
				return v, "unwatched"
			}
		}
		return v, "watched"
	}
	for _, key := range keys {
		if m.watched[key] || v.Kind == "episode" && m.watched[seasonFromEpisodeKey(key)] {
			return v, "watched"
		}
	}
	return v, "unwatched"
}
func seasonFromEpisodeKey(key string) string {
	i := strings.LastIndex(key, ":s")
	if i < 0 || len(key) < i+4 {
		return ""
	}
	return "season" + strings.TrimPrefix(key[:i+4], "episode")
}
func mediaKeys(v mediaInfo) []string {
	if v.Kind == "episode" {
		return episodeKeys(v)
	}
	keys := []string{}
	for provider, id := range map[string]string{"tmdb": v.TMDB, "tvdb": v.TVDB, "imdb": v.IMDB} {
		if id != "" {
			keys = append(keys, v.Kind+":"+provider+":"+id)
		}
	}
	return keys
}
func episodeKeys(v mediaInfo) []string {
	keys := []string{}
	for provider, id := range map[string]string{"tmdb": v.TMDB, "tvdb": v.TVDB, "imdb": v.IMDB} {
		if id != "" {
			keys = append(keys, fmt.Sprintf("episode:%s:%s:s%02de%02d", provider, id, v.Season, v.Episode))
		}
	}
	return keys
}
func (m *mediaState) mediaStatus(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	jsonOut(w, map[string]any{"radarr": m.radarr.configured(), "sonarr": m.sonarr.configured(), "silo": m.silo.configured(), "items": len(m.byPath) + len(m.byFolder), "sources": m.sources, "synced_at": m.catalogAt, "error": m.catalogError})
}
func (s *Server) mediaStatus(w http.ResponseWriter, r *http.Request) { s.mdblist.mediaStatus(w, r) }
func (s *Server) mdblistStatus(w http.ResponseWriter, r *http.Request) {
	m := s.mdblist
	m.mu.RLock()
	defer m.mu.RUnlock()
	jsonOut(w, map[string]any{"configured": m.mdblistKey != "", "synced_at": m.watchedAt, "items": len(m.watched), "error": m.watchedError})
}
func (s *Server) mdblistSync(w http.ResponseWriter, r *http.Request) {
	m := s.mdblist
	_, watched := m.configured()
	if !watched {
		problem(w, 409, fmt.Errorf("configure MDBList in Settings"))
		return
	}
	if e := m.syncWatched(r.Context()); e != nil {
		problem(w, 502, e)
		return
	}
	s.mdblistStatus(w, r)
}
func (s *Server) mediaSync(w http.ResponseWriter, r *http.Request) {
	m := s.mdblist
	catalog, _ := m.configured()
	if !catalog {
		problem(w, 409, fmt.Errorf("configure Radarr, Sonarr, or Silo"))
		return
	}
	if e := m.syncCatalog(r.Context()); e != nil {
		problem(w, 502, e)
		return
	}
	s.mediaStatus(w, r)
}
func (s *Server) mediaSyncOnce(ctx context.Context) {
	catalog, watched := s.mdblist.configured()
	if catalog {
		_ = s.mdblist.syncCatalog(ctx)
	}
	if watched {
		_ = s.mdblist.syncWatched(ctx)
	}
}
func (s *Server) mediaSyncLoop(ctx context.Context) {
	run := func() { c, cancel := context.WithTimeout(ctx, 10*time.Minute); defer cancel(); s.mediaSyncOnce(c) }
	run()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
func (m *mediaState) get(ctx context.Context, base, path, key, header string, out any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set(header, key)
	}
	resp, e := m.client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out); e != nil {
		return fmt.Errorf("%s: %w", req.URL.Host, e)
	}
	return nil
}
func (m *mediaState) syncWatched(ctx context.Context) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	keys := map[string]bool{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 1000; page++ {
		query := url.Values{"apikey": {m.mdblistKey}, "limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var body struct {
			Movies, Shows, Seasons, Episodes []json.RawMessage
			Pagination                       struct {
				NextCursor string `json:"next_cursor"`
			}
		}
		e := m.get(ctx, m.mdblistBase, "/sync/watched?"+query.Encode(), "", "", &body)
		if e != nil {
			m.mu.Lock()
			m.watchedError = e.Error()
			m.mu.Unlock()
			return e
		}
		for _, b := range body.Movies {
			addWatched(keys, "movie", b)
		}
		for _, b := range body.Shows {
			addWatched(keys, "show", b)
		}
		for _, b := range body.Episodes {
			addWatched(keys, "episode", b)
		}
		for _, b := range body.Seasons {
			addWatched(keys, "season", b)
		}
		next := body.Pagination.NextCursor
		if next == "" {
			m.mu.Lock()
			m.watched = keys
			m.watchedAt = time.Now()
			m.watchedError = ""
			m.mu.Unlock()
			return nil
		}
		if seen[next] {
			return errors.New("MDBList repeated a pagination cursor")
		}
		seen[next] = true
		cursor = next
	}
	return errors.New("MDBList watched history exceeded 1000 pages")
}
func addWatched(keys map[string]bool, kind string, raw json.RawMessage) {
	var row map[string]any
	if json.Unmarshal(raw, &row) != nil {
		return
	}
	item := obj(row[kind])
	if len(item) == 0 {
		item = row
	}
	ids := obj(item["ids"])
	if len(ids) == 0 {
		ids = obj(row["ids"])
	}
	v := mediaInfo{Kind: kind, TMDB: idString(ids["tmdb"]), TVDB: idString(ids["tvdb"]), IMDB: idString(ids["imdb"])}
	if kind == "episode" || kind == "season" {
		show := obj(row["show"])
		if len(show) == 0 {
			show = obj(item["show"])
		}
		if len(show) == 0 {
			show = obj(row["series"])
		}
		sid := obj(show["ids"])
		if len(sid) > 0 {
			v.TVDB = idString(sid["tvdb"])
			v.TMDB = idString(sid["tmdb"])
			v.IMDB = idString(sid["imdb"])
		}
		seasonRaw, hasSeason := item["season"]
		if !hasSeason {
			seasonRaw, hasSeason = item["season_number"]
		}
		if kind == "season" && !hasSeason {
			seasonRaw, hasSeason = item["number"]
		}
		if !hasSeason {
			return
		}
		v.Season = intValue(seasonRaw)
		if kind == "season" {
			for _, k := range seasonKeys(v) {
				keys[k] = true
			}
			return
		}
		episodeRaw, hasEpisode := item["number"]
		if !hasEpisode {
			episodeRaw, hasEpisode = item["episode_number"]
		}
		if !hasEpisode {
			episodeRaw, hasEpisode = item["episode"]
		}
		if !hasEpisode {
			return
		}
		v.Episode = intValue(episodeRaw)
		for _, k := range episodeKeys(v) {
			keys[k] = true
		}
		return
	}
	for _, k := range mediaKeys(v) {
		keys[k] = true
	}
	if kind == "show" {
		for _, s := range array(row["seasons"]) {
			season := obj(s)
			sv := v
			sv.Season = intValue(season["number"])
			if len(array(season["episodes"])) == 0 {
				for _, k := range seasonKeys(sv) {
					keys[k] = true
				}
			}
			for _, ep := range array(season["episodes"]) {
				ev := sv
				ev.Episode = intValue(obj(ep)["number"])
				for _, k := range episodeKeys(ev) {
					keys[k] = true
				}
			}
		}
	}
}
func seasonKeys(v mediaInfo) []string {
	keys := []string{}
	for provider, id := range map[string]string{"tmdb": v.TMDB, "tvdb": v.TVDB, "imdb": v.IMDB} {
		if id != "" {
			keys = append(keys, fmt.Sprintf("season:%s:%s:s%02d", provider, id, v.Season))
		}
	}
	return keys
}
func array(v any) []any        { x, _ := v.([]any); return x }
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x > 0 {
			return strconv.FormatInt(int64(x), 10)
		}
	case json.Number:
		return x.String()
	}
	return ""
}
func intValue(v any) int { x, _ := strconv.Atoi(idString(v)); return x }
func (m *mediaState) syncCatalog(ctx context.Context) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	files := map[string]mediaInfo{}
	folders := map[string]mediaInfo{}
	counts := map[string]int{}
	failures := []string{}
	for _, source := range []struct {
		name    string
		enabled bool
		load    func(context.Context, map[string]mediaInfo, map[string]mediaInfo) (int, error)
	}{
		{"silo", m.silo.configured(), m.loadSilo}, {"radarr", m.radarr.configured(), m.loadRadarr}, {"sonarr", m.sonarr.configured(), m.loadSonarr},
	} {
		if !source.enabled {
			continue
		}
		f := map[string]mediaInfo{}
		d := map[string]mediaInfo{}
		n, e := source.load(ctx, f, d)
		if e != nil {
			failures = append(failures, e.Error())
			continue
		}
		for path, v := range f {
			files[path] = v
		}
		for path, v := range d {
			folders[path] = v
		}
		counts[source.name] = n
	}
	message := strings.Join(failures, "; ")
	m.mu.Lock()
	m.catalogError = message
	if len(counts) > 0 {
		m.byPath = files
		m.byFolder = folders
		m.sources = counts
		m.catalogAt = time.Now()
	}
	m.mu.Unlock()
	if len(counts) == 0 && len(failures) > 0 {
		return errors.New(message)
	}
	return nil
}
func (m *mediaState) loadRadarr(ctx context.Context, files, folders map[string]mediaInfo) (int, error) {
	var movies []struct {
		Title, Path, ImdbID string
		Year, TmdbID        int
		MovieFile           *struct{ Path string }
	}
	e := m.get(ctx, m.radarr.URL, "/api/v3/movie", m.radarr.Key, "X-Api-Key", &movies)
	if e != nil {
		return 0, fmt.Errorf("Radarr: %w", e)
	}
	count := 0
	for _, movie := range movies {
		v := mediaInfo{Kind: "movie", Title: movie.Title, Year: strconv.Itoa(movie.Year), TMDB: strconv.Itoa(movie.TmdbID), IMDB: movie.ImdbID, MatchSource: "Radarr"}
		if movie.TmdbID == 0 {
			v.TMDB = ""
		}
		if movie.Year == 0 {
			v.Year = ""
		}
		if p := m.radarr.canonical(movie.Path); p != "" {
			folders[p] = v
			count++
		}
		if movie.MovieFile != nil {
			if p := m.radarr.canonical(movie.MovieFile.Path); p != "" {
				files[p] = v
			}
		}
	}
	return count, nil
}
func (m *mediaState) loadSonarr(ctx context.Context, files, folders map[string]mediaInfo) (int, error) {
	var series []struct {
		ID, Year, TvdbID, TmdbID int
		Title, Path, ImdbID      string
	}
	if e := m.get(ctx, m.sonarr.URL, "/api/v3/series", m.sonarr.Key, "X-Api-Key", &series); e != nil {
		return 0, fmt.Errorf("Sonarr series: %w", e)
	}
	byID := map[int]mediaInfo{}
	for _, show := range series {
		v := mediaInfo{Kind: "show", Title: show.Title, TVDB: strconv.Itoa(show.TvdbID), TMDB: strconv.Itoa(show.TmdbID), IMDB: show.ImdbID, MatchSource: "Sonarr"}
		if show.TvdbID == 0 {
			v.TVDB = ""
		}
		if show.TmdbID == 0 {
			v.TMDB = ""
		}
		if show.Year > 0 {
			v.Year = strconv.Itoa(show.Year)
		}
		byID[show.ID] = v
		if p := m.sonarr.canonical(show.Path); p != "" {
			folders[p] = v
		}
	}
	var episodes []struct{ ID, SeriesID, EpisodeFileID, SeasonNumber, EpisodeNumber int }
	if e := m.get(ctx, m.sonarr.URL, "/api/v3/episode", m.sonarr.Key, "X-Api-Key", &episodes); e != nil {
		return 0, fmt.Errorf("Sonarr episodes: %w", e)
	}
	byFile := map[int][]mediaInfo{}
	for _, ep := range episodes {
		if ep.EpisodeFileID == 0 {
			continue
		}
		v, ok := byID[ep.SeriesID]
		if !ok {
			continue
		}
		v.Kind = "episode"
		v.Season = ep.SeasonNumber
		v.Episode = ep.EpisodeNumber
		byFile[ep.EpisodeFileID] = append(byFile[ep.EpisodeFileID], v)
	}
	var episodeFiles []struct {
		ID, SeriesID int
		Path         string
	}
	if e := m.get(ctx, m.sonarr.URL, "/api/v3/episodefile", m.sonarr.Key, "X-Api-Key", &episodeFiles); e != nil {
		return 0, fmt.Errorf("Sonarr episode files: %w", e)
	}
	for _, file := range episodeFiles {
		v := byFile[file.ID]
		if len(v) == 0 {
			continue
		}
		p := m.sonarr.canonical(file.Path)
		if p == "" {
			continue
		}
		item := v[0]
		if len(v) > 1 {
			for _, ep := range v {
				item.episodeKeyGroups = append(item.episodeKeyGroups, episodeKeys(ep))
			}
		}
		files[p] = item
	}
	return len(series) + len(episodeFiles), nil
}
func (m *mediaState) loadSilo(ctx context.Context, files, folders map[string]mediaInfo) (int, error) {
	count := 0
	for _, kind := range []string{"movie", "series"} {
		cursor := ""
		seen := map[string]bool{}
		for page := 0; page < 100; page++ {
			q := url.Values{"type": {kind}, "limit": {"100"}, "skip_total": {"true"}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			var listing struct {
				Items []struct {
					ContentID string `json:"content_id"`
					Type      string `json:"type"`
					Title     string `json:"title"`
				}
				Page struct {
					HasMore    bool   `json:"has_more"`
					NextCursor string `json:"next_cursor"`
				}
			}
			if e := m.get(ctx, m.silo.URL, "/api/v2/catalog?"+q.Encode(), "Bearer "+m.silo.Key, "Authorization", &listing); e != nil {
				return 0, fmt.Errorf("Silo catalog: %w", e)
			}
			for _, item := range listing.Items {
				if item.ContentID == "" || item.Type != kind {
					continue
				}
				var detail struct {
					ContentID   string   `json:"content_id"`
					Type        string   `json:"type"`
					Title       string   `json:"title"`
					ImdbID      string   `json:"imdb_id"`
					TmdbID      string   `json:"tmdb_id"`
					TvdbID      string   `json:"tvdb_id"`
					Year        int      `json:"year"`
					FolderPaths []string `json:"folder_paths"`
					Versions    []struct {
						FilePath string `json:"file_path"`
					} `json:"versions"`
				}
				path := "/api/v2/catalog/items/" + url.PathEscape(item.ContentID)
				if e := m.get(ctx, m.silo.URL, path, "Bearer "+m.silo.Key, "Authorization", &detail); e != nil {
					return 0, fmt.Errorf("Silo item %s: %w", item.ContentID, e)
				}
				v := mediaInfo{Kind: "movie", Title: detail.Title, TMDB: detail.TmdbID, TVDB: detail.TvdbID, IMDB: detail.ImdbID, MatchSource: "Silo"}
				if kind == "series" {
					v.Kind = "show"
				}
				if detail.Year > 0 {
					v.Year = strconv.Itoa(detail.Year)
				}
				for _, folder := range detail.FolderPaths {
					if p := m.silo.canonical(folder); p != "" {
						folders[p] = v
					}
				}
				for _, version := range detail.Versions {
					if p := m.silo.canonical(version.FilePath); p != "" {
						files[p] = v
					}
				}
				count++
			}
			if !listing.Page.HasMore {
				break
			}
			next := listing.Page.NextCursor
			if next == "" || seen[next] {
				return 0, errors.New("Silo catalog pagination did not advance")
			}
			seen[next] = true
			cursor = next
			if page == 99 {
				return 0, errors.New("Silo catalog exceeded 100 pages")
			}
		}
	}
	return count, nil
}
