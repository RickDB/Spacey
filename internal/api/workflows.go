package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"spacey/internal/database"
	"spacey/internal/model"
	"strconv"
	"strings"
	"time"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	scan := r.URL.Query().Get("scan_id")
	if scan == "" {
		problem(w, 400, fmt.Errorf("scan_id required"))
		return
	}
	v, e := s.DB.Overview(r.Context(), scan)
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, v)
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	scan := r.URL.Query().Get("scan_id")
	if scan == "" {
		problem(w, 400, fmt.Errorf("scan_id required"))
		return
	}
	min, _ := strconv.ParseInt(r.URL.Query().Get("min_bytes"), 10, 64)
	limit := intParam(r, "limit", 200)
	if limit < 1 || limit > 500 {
		limit = 200
	}
	watched := r.URL.Query().Get("watched")
	mediaKind := r.URL.Query().Get("media")
	out := make([]map[string]any, 0, limit)
	var afterSize, afterID int64
	for len(out) < limit {
		batch, e := s.DB.SearchNodesPage(r.Context(), scan, r.URL.Query().Get("q"), r.URL.Query().Get("type"), min, afterSize, afterID, 1000)
		if e != nil {
			problem(w, 500, e)
			return
		}
		if len(batch) == 0 {
			break
		}
		for _, n := range batch {
			afterSize = n.LogicalSize
			afterID = n.ID
			media, status := s.mdblist.resolve(n)
			if mediaKind != "" && media.Kind != mediaKind {
				continue
			}
			if watched == "yes" && status != "watched" || watched == "no" && status != "unwatched" || watched == "unknown" && status != "unknown" {
				continue
			}
			out = append(out, map[string]any{"node": n, "media": media, "watch_status": status})
			if len(out) >= limit {
				break
			}
		}
		if len(batch) < 1000 {
			break
		}
	}

	jsonOut(w, out)
}
func (s *Server) filters(w http.ResponseWriter, r *http.Request) {
	v, e := s.DB.Filters(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, v)
}
func (s *Server) addFilter(w http.ResponseWriter, r *http.Request) {
	var v database.Filter
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&v); e != nil {
		problem(w, 400, e)
		return
	}
	if strings.TrimSpace(v.Name) == "" || !json.Valid(v.Criteria) {
		problem(w, 400, fmt.Errorf("name and valid criteria required"))
		return
	}
	v, e := s.DB.AddFilter(r.Context(), v)
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOutStatus(w, v, 201)
}
func (s *Server) deleteFilter(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e == nil {
		e = s.DB.DeleteFilter(r.Context(), id)
	}
	if e != nil {
		problem(w, 400, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) schedules(w http.ResponseWriter, r *http.Request) {
	v, e := s.DB.Schedules(r.Context())
	if e != nil {
		problem(w, 500, e)
		return
	}
	jsonOut(w, v)
}
func (s *Server) addSchedule(w http.ResponseWriter, r *http.Request) {
	var v database.Schedule
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&v); e != nil {
		problem(w, 400, e)
		return
	}
	if v.StorageSourceID < 1 || v.IntervalMinutes < 15 || strings.TrimSpace(v.Name) == "" {
		problem(w, 400, fmt.Errorf("source, name, and interval of at least 15 minutes required"))
		return
	}
	v.Enabled = true
	v, e := s.DB.AddSchedule(r.Context(), v)
	if e != nil {
		problem(w, 400, e)
		return
	}
	jsonOutStatus(w, v, 201)
}
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e == nil {
		e = s.DB.DeleteSchedule(r.Context(), id)
	}
	if e != nil {
		problem(w, 400, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, e := s.DB.DueSchedules(ctx)
			if e != nil {
				continue
			}
			for _, job := range rows {
				src, e := s.DB.Source(ctx, job.StorageSourceID)
				if e != nil || !src.Available {
					continue
				}
				s.mu.RLock()
				busy := false
				for _, p := range s.progress {
					if p.Status == "running" && p.Root == src.CanonicalRoot {
						busy = true
						break
					}
				}
				s.mu.RUnlock()
				if busy {
					continue
				}
				id := uuid()
				if s.DB.CreateScan(ctx, id, src.ID, src.CanonicalRoot) != nil {
					continue
				}
				if s.DB.AdvanceSchedule(ctx, job.ID, job.IntervalMinutes) != nil {
					continue
				}
				runCtx, cancel := context.WithCancel(ctx)
				s.mu.Lock()
				s.cancels[id] = cancel
				s.progress[id] = model.Progress{ScanID: id, Root: src.CanonicalRoot, Status: "running", StartedAt: time.Now()}
				s.mu.Unlock()
				go s.runScan(runCtx, id, src)
			}
		}
	}
}

type mediaInfo struct {
	Kind             string `json:"kind"`
	Title            string `json:"title"`
	Year             string `json:"year,omitempty"`
	Season           int    `json:"season,omitempty"`
	Episode          int    `json:"episode,omitempty"`
	TMDB             string `json:"tmdb,omitempty"`
	TVDB             string `json:"tvdb,omitempty"`
	IMDB             string `json:"imdb,omitempty"`
	MatchSource      string `json:"match_source,omitempty"`
	episodeKeyGroups [][]string
}

var episodePattern = regexp.MustCompile(`(?i)[. _-]s(\d{1,2})e(\d{1,3})`)
var yearPattern = regexp.MustCompile(`\((19\d\d|20\d\d)\)`)

func parseMedia(n model.Node) mediaInfo {
	base := n.Name
	if n.Type == "file" {
		base = strings.TrimSuffix(base, n.Extension)
	}
	m := mediaInfo{Kind: "other", Title: base}
	path := strings.ToLower(n.CanonicalPath)
	if n.Type == "file" && !(strings.Contains(" .mkv .mp4 .avi .mov .m4v .ts .wmv ", " "+n.Extension+" ")) {
		return m
	}
	if hit := episodePattern.FindStringSubmatchIndex(base); hit != nil {
		parts := episodePattern.FindStringSubmatch(base)
		m.Kind = "episode"
		m.Season, _ = strconv.Atoi(parts[1])
		m.Episode, _ = strconv.Atoi(parts[2])
		m.Title = cleanTitle(base[:hit[0]])
		if m.Title == "" {
			segments := strings.Split(n.RelativePath, string(os.PathSeparator))
			if len(segments) > 1 {
				m.Title = cleanTitle(segments[0])
			}
		}
		return m
	}
	if strings.Contains(path, "/tv/") || strings.Contains(path, "/series/") || strings.Contains(path, "/shows/") {
		m.Kind = "show"
	} else {
		m.Kind = "movie"
	}
	if y := yearPattern.FindStringSubmatch(base); len(y) > 1 {
		m.Year = y[1]
		base = strings.Replace(base, y[0], "", 1)
	}
	m.Title = cleanTitle(base)
	return m
}
func cleanTitle(s string) string {
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.TrimSpace(s)
}
func normalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
