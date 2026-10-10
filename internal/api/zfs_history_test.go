package api

import (
	"net/http/httptest"
	"testing"
)

func TestZFSHistoryRejectsInvalidQuery(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/api/v1/zfs/history?kind=pool&id=0&range=7d", "/api/v1/zfs/history?kind=pool&id=1&range=all", "/api/v1/zfs/history?kind=dataset&id=bad&range=24h"} {
		w := httptest.NewRecorder()
		s.zfsHistory(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("%s: got %d", path, w.Code)
		}
	}
}
