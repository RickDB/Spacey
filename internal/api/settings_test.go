package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestSettingsStatusRedactsSecrets(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("long-password-here"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{settings: appSettings{AdminHash: string(hash), ScanWorkers: 8, Radarr: mediaService{URL: "http://radarr:7878", Key: "secret-radarr"}, MDBListKey: "secret-mdblist"}}
	if !s.validAdmin("long-password-here") || s.validAdmin("wrong-password") {
		t.Fatal("administrator authentication failed")
	}
	w := httptest.NewRecorder()
	s.settingsStatus(w, httptest.NewRequest("GET", "/api/v1/settings", nil))
	body := w.Body.String()
	if strings.Contains(body, "secret-radarr") || strings.Contains(body, "secret-mdblist") || strings.Contains(body, string(hash)) {
		t.Fatal("settings response exposed a secret")
	}
	var got struct {
		SetupComplete bool `json:"setup_complete"`
		Radarr        struct {
			HasKey bool `json:"has_key"`
		} `json:"radarr"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.SetupComplete || !got.Radarr.HasKey {
		t.Fatal("settings status omitted configured state")
	}
}
func TestSetupRequiresAdministratorAndSource(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	body := `{"admin_password":"short","scan_workers":8}`
	s.saveSettings(w, httptest.NewRequest("POST", "/api/v1/setup", strings.NewReader(body)))
	if w.Code != 400 {
		t.Fatalf("got status %d: %s", w.Code, w.Body.String())
	}
}
