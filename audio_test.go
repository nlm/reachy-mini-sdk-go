package reachymini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestReadAudioParameterEscapesName(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_ = json.NewEncoder(w).Encode(ReadAudioParameterResponse{Name: "vol/master", Values: []float64{0.5}})
	}))
	defer srv.Close()

	c := New(srv.URL)
	resp, err := c.ReadAudioParameter(context.Background(), "vol/master")
	if err != nil {
		t.Fatalf("ReadAudioParameter: %v", err)
	}
	want := "/api/audio/config/parameter/" + url.PathEscape("vol/master")
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if len(resp.Values) != 1 || resp.Values[0] != 0.5 {
		t.Errorf("values = %v, want [0.5]", resp.Values)
	}
}

func TestApplyAudioConfigSendsConfigAndVerifyReturnsApplied(t *testing.T) {
	var got applyAudioConfigRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"applied":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	config := []AudioParamPair{{Name: "master", Values: []float64{1, 2}}}
	applied, err := c.ApplyAudioConfig(context.Background(), config, true)
	if err != nil {
		t.Fatalf("ApplyAudioConfig: %v", err)
	}
	if !applied {
		t.Error("applied = false, want true")
	}
	if !got.Verify {
		t.Error("request Verify = false, want true")
	}
	if len(got.Config) != 1 || got.Config[0].Name != "master" {
		t.Errorf("request Config = %+v, want it to echo the input", got.Config)
	}
}
