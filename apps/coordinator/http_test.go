package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPRejectsUnauthorizedAndTrailingJSON(t *testing.T) {
	s := setup(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("anonymous status=%d", w.Code)
	}
	body, _ := json.Marshal(samplePlan())
	req = httptest.NewRequest(http.MethodPost, "/v1/plans", bytes.NewReader(append(body, []byte(` {}`)...)))
	req.Header.Set("Authorization", "Bearer controller")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("trailing JSON status=%d", w.Code)
	}
}
func TestKnownWorkerCapabilityMismatchRejectsWholePlan(t *testing.T) {
	s := setup(t)
	reg := `{"manifest":{"device_id":"w"},"capabilities":[]}`
	if _, e := s.db.Exec(`INSERT INTO devices(id,registration) VALUES('w',?)`, reg); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Submit(samplePlan()); e == nil {
		t.Fatal("known incapable target accepted")
	}
	snap, e := s.Snapshot()
	if e != nil || len(snap.Plans) != 0 {
		t.Fatal("partial Plan committed")
	}
}
