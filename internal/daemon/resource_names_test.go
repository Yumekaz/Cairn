package daemon

import (
	"encoding/json"
	"github.com/yumekaz/cairn/internal/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceNamesCannotEscapeStateDirectories(t *testing.T) {
	for _, name := range []string{"../escape", "a/../../escape", "/absolute", `a\escape`, ".", "..", "bad:name", "bad\x00name"} {
		t.Run(name, func(t *testing.T) {
			s, st, cleanup := setupDaemonStore(t)
			defer cleanup()
			body, _ := json.Marshal(map[string]string{"name": name, "mount_path": "/data"})
			response := httptest.NewRecorder()
			s.handleCreateVolume(response, httptest.NewRequest("POST", "/volumes", strings.NewReader(string(body))))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("unsafe volume accepted: %d %s", response.Code, response.Body)
			}
			volumes, err := st.ListVolumes()
			if err != nil || len(volumes) != 0 {
				t.Fatalf("unexpected volume state: %+v %v", volumes, err)
			}
			if _, err := os.Stat(filepath.Join(s.config.DataDir, "escape")); !os.IsNotExist(err) {
				t.Fatal("path traversal wrote outside volume root")
			}
			for _, cfg := range []api.ServiceConfig{{Name: name}, {Name: "safe-service", Volumes: []api.VolumeConfig{{Name: name, MountPath: "/data"}}}} {
				encoded, _ := json.Marshal(cfg)
				response = httptest.NewRecorder()
				s.handleCreateService(response, httptest.NewRequest("POST", "/services", strings.NewReader(string(encoded))))
				if response.Code != http.StatusBadRequest {
					t.Fatalf("unsafe service/volume accepted: %d", response.Code)
				}
			}
		})
	}
}

func TestValidResourceNames(t *testing.T) {
	for _, name := range []string{"a", "notes-proof-12345678", "database_data", "Release.v2"} {
		if !api.ValidResourceName(name) {
			t.Errorf("valid name rejected: %s", name)
		}
	}
}
