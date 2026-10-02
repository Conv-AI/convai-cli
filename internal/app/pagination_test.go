package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAllPreservesOptionalTotalsAndPageProgress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pages  []string
		items  int
		total  any
		offset int
		fail   bool
	}{
		{"unknown empty", []string{`{"items":[]}`}, 0, nil, 0, false},
		{"unknown short", []string{`{"items":[{"character_id":"a"}]}`}, 1, nil, 0, false},
		{"unknown exact pages", []string{`{"items":[{"character_id":"a"},{"character_id":"b"}]}`, `{"items":[{"character_id":"c"},{"character_id":"d"}]}`, `{"items":[]}`}, 4, nil, 0, false},
		{"unknown short tail and offset", []string{`{"items":[{"character_id":"a"},{"character_id":"b"}]}`, `{"items":[{"character_id":"c"}]}`}, 3, nil, 5, false},
		{"known becomes unknown", []string{`{"items":[{"character_id":"a"},{"character_id":"b"}],"total":4}`, `{"items":[{"character_id":"c"},{"character_id":"d"}]}`, `{"items":[]}`}, 4, nil, 0, false},
		{"unknown becomes known", []string{`{"items":[{"character_id":"a"},{"character_id":"b"}]}`, `{"items":[{"character_id":"c"}],"total":3}`}, 3, float64(3), 0, false},
		{"known completed", []string{`{"items":[{"character_id":"a"},{"character_id":"b"}],"total":3}`, `{"items":[{"character_id":"c"}],"total":3}`}, 3, float64(3), 0, false},
		{"known premature empty", []string{`{"items":[],"total":3}`}, 0, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, offset := 0, tc.offset
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("offset") != fmt.Sprint(offset) || r.URL.Query().Get("limit") != "2" {
					t.Errorf("pagination context changed: %s", r.URL.RawQuery)
				}
				if calls >= len(tc.pages) {
					t.Error("unexpected extra page request")
					w.WriteHeader(500)
					return
				}
				page := tc.pages[calls]
				calls++
				var decoded struct {
					Items []any `json:"items"`
				}
				_ = json.Unmarshal([]byte(page), &decoded)
				offset += len(decoded.Items)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, page)
			}))
			defer server.Close()
			stdout, _, err := execute(t, server, strings.NewReader(""), "character", "list", "--all", "--limit", "2", "--offset", fmt.Sprint(tc.offset), "--output", "json")
			if calls != len(tc.pages) || (err != nil) != tc.fail {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
			if tc.fail {
				return
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if len(result["items"].([]any)) != tc.items || result["total"] != tc.total {
				t.Fatalf("result=%v", result)
			}
		})
	}
}

func TestUnknownTotalPaginationStillHasRequestBudget(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"character_id":"fixture"}]}`)
	}))
	defer server.Close()
	_, _, err := execute(t, server, strings.NewReader(""), "character", "list", "--all", "--limit", "1")
	if calls != 1000 || err == nil || !strings.Contains(err.Error(), "exceeded 1000 pages") {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}
