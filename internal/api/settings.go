package api

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type appSettings struct {
	AdminHash   string       `json:"admin_hash"`
	ScanWorkers int          `json:"scan_workers"`
	EnableZFS   bool         `json:"enable_zfs"`
	Radarr      mediaService `json:"radarr"`
	Sonarr      mediaService `json:"sonarr"`
	Silo        mediaService `json:"silo"`
	MDBListKey  string       `json:"mdblist_key"`
}
type settingsInput struct {
	AdminPassword string       `json:"admin_password"`
	ScanWorkers   int          `json:"scan_workers"`
	EnableZFS     bool         `json:"enable_zfs"`
	Radarr        mediaService `json:"radarr"`
	Sonarr        mediaService `json:"sonarr"`
	Silo          mediaService `json:"silo"`
	MDBListKey    string       `json:"mdblist_key"`
	RemoveMDBList bool         `json:"remove_mdblist"`
	Source        *struct {
		Name          string `json:"name"`
		CanonicalRoot string `json:"canonical_root"`
		ContainerRoot string `json:"container_root"`
	} `json:"source"`
}

func (s *Server) validAdmin(password string) bool {
	s.mu.RLock()
	hash := s.settings.AdminHash
	s.mu.RUnlock()
	return hash != "" && password != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func (s *Server) settingsStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	v := s.settings
	s.mu.RUnlock()
	jsonOut(w, map[string]any{"setup_complete": v.AdminHash != "", "scan_workers": v.ScanWorkers, "enable_zfs": v.EnableZFS, "radarr": map[string]any{"url": v.Radarr.URL, "remote_root": v.Radarr.RemoteRoot, "canonical_root": v.Radarr.CanonicalRoot, "has_key": v.Radarr.Key != ""}, "sonarr": map[string]any{"url": v.Sonarr.URL, "remote_root": v.Sonarr.RemoteRoot, "canonical_root": v.Sonarr.CanonicalRoot, "has_key": v.Sonarr.Key != ""}, "silo": map[string]any{"url": v.Silo.URL, "remote_root": v.Silo.RemoteRoot, "canonical_root": v.Silo.CanonicalRoot, "has_key": v.Silo.Key != ""}, "mdblist_has_key": v.MDBListKey != ""})
}
func validService(name string, v mediaService) error {
	if v.URL == "" && v.Key == "" && v.RemoteRoot == "" && v.CanonicalRoot == "" {
		return nil
	}
	u, e := url.Parse(v.URL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s requires an HTTP(S) URL", name)
	}
	if v.Key == "" {
		return fmt.Errorf("%s requires an API key", name)
	}
	if (v.RemoteRoot == "") != (v.CanonicalRoot == "") {
		return fmt.Errorf("%s path mapping needs both roots", name)
	}
	if v.RemoteRoot != "" && (!filepath.IsAbs(v.RemoteRoot) || !filepath.IsAbs(v.CanonicalRoot)) {
		return fmt.Errorf("%s path roots must be absolute", name)
	}
	return nil
}
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	initial := r.URL.Path == "/api/v1/setup"
	s.mu.RLock()
	old := s.settings
	s.mu.RUnlock()
	if initial && old.AdminHash != "" {
		problem(w, 409, fmt.Errorf("setup is already complete"))
		return
	}
	if !initial && !s.validAdmin(r.Header.Get("X-Spacey-Admin-Token")) {
		problem(w, 401, fmt.Errorf("invalid administrator password"))
		return
	}
	var in settingsInput
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); e != nil {
		problem(w, 400, e)
		return
	}
	if initial && (len(in.AdminPassword) < 12 || in.Source == nil) {
		problem(w, 400, fmt.Errorf("setup requires an administrator password of at least 12 characters and a storage source"))
		return
	}
	if !initial && in.AdminPassword != "" && len(in.AdminPassword) < 12 {
		problem(w, 400, fmt.Errorf("administrator password must have at least 12 characters"))
		return
	}
	if in.ScanWorkers < 1 || in.ScanWorkers > 64 {
		problem(w, 400, fmt.Errorf("scan workers must be between 1 and 64"))
		return
	}
	if in.Source != nil && (strings.TrimSpace(in.Source.Name) == "" || !filepath.IsAbs(in.Source.CanonicalRoot) || !filepath.IsAbs(in.Source.ContainerRoot)) {
		problem(w, 400, fmt.Errorf("storage source needs a name and absolute paths"))
		return
	}
	for _, v := range []*mediaService{&in.Radarr, &in.Sonarr, &in.Silo} {
		v.URL = strings.TrimRight(v.URL, "/")
	}
	next := appSettings{ScanWorkers: in.ScanWorkers, EnableZFS: in.EnableZFS, Radarr: in.Radarr, Sonarr: in.Sonarr, Silo: in.Silo, MDBListKey: in.MDBListKey}
	if !initial {
		next.AdminHash = old.AdminHash
		if next.Radarr.Key == "" && next.Radarr.URL != "" {
			next.Radarr.Key = old.Radarr.Key
		}
		if next.Sonarr.Key == "" && next.Sonarr.URL != "" {
			next.Sonarr.Key = old.Sonarr.Key
		}
		if next.Silo.Key == "" && next.Silo.URL != "" {
			next.Silo.Key = old.Silo.Key
		}
		if in.RemoveMDBList {
			next.MDBListKey = ""
		} else if next.MDBListKey == "" {
			next.MDBListKey = old.MDBListKey
		}
	}
	if next.Radarr.URL == "" {
		next.Radarr = mediaService{}
	}
	if next.Sonarr.URL == "" {
		next.Sonarr = mediaService{}
	}
	if next.Silo.URL == "" {
		next.Silo = mediaService{}
	}
	for name, v := range map[string]mediaService{"Radarr": next.Radarr, "Sonarr": next.Sonarr, "Silo": next.Silo} {
		if e := validService(name, v); e != nil {
			problem(w, 400, e)
			return
		}
	}
	if in.AdminPassword != "" {
		hash, e := bcrypt.GenerateFromPassword([]byte(in.AdminPassword), bcrypt.DefaultCost)
		if e != nil {
			problem(w, 500, e)
			return
		}
		next.AdminHash = string(hash)
	}
	if in.Source != nil {
		if _, e := s.DB.CreateSource(r.Context(), s.HostID, in.Source.Name, filepath.Clean(in.Source.CanonicalRoot), filepath.Clean(in.Source.ContainerRoot)); e != nil {
			problem(w, 500, e)
			return
		}
	}
	saved, e := s.DB.SaveAppSettings(r.Context(), next, initial)
	if e != nil {
		problem(w, 500, e)
		return
	}
	if !saved {
		problem(w, 409, fmt.Errorf("settings changed; reload and try again"))
		return
	}
	s.mu.Lock()
	s.settings = next
	s.Workers = next.ScanWorkers
	s.mu.Unlock()
	s.mdblist.applySettings(next)
	if next.EnableZFS && !old.EnableZFS {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			s.runZFSDiscovery(ctx)
		}()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.mediaSyncOnce(ctx)
	}()
	s.settingsStatus(w, r)
}
