package observation_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/palworld"
)

func TestPalworldSettingsNumbersReachCanonicalizationExactly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"safe":1.0,"tiny":1e-400,"large_fraction":9007199254740992.1,"negative_zero":-0.0}`))
	}))
	defer server.Close()
	settings, err := palworld.New(server.URL, "secret", time.Second).Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"safe", "tiny", "large_fraction", "negative_zero"} {
		if _, ok := settings.Values[key].(json.Number); !ok {
			t.Fatalf("%s lost json.Number: %#v", key, settings.Values[key])
		}
	}
	recorder := &serverRecorderFake{}
	if err := newServerService(recorder).RecordSettings(t.Context(), time.Now(), settings); err != nil {
		t.Fatal(err)
	}
	if len(recorder.documentCalls) != 1 {
		t.Fatalf("calls=%#v", recorder.documentCalls)
	}
	want := `{"large_fraction":9007199254740992.1,"negative_zero":0,"safe":1,"tiny":0.` + strings.Repeat("0", 399) + `1}`
	if got := string(recorder.documentCalls[0].canonical); got != want {
		t.Fatalf("canonical=%s want=%s", got, want)
	}
}
