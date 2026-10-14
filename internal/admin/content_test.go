package admin

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// The pagination parser is the content contract's discipline (api's
// contentPageParams twin): absent means the default, in-bounds values
// pass, anything malformed or out of range is the one named bad
// request — never a silent clamp.
func TestContentPageParams(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		pageNo   int
		pageSize int
		wantErr  bool
	}{
		{name: "absent means defaults", query: "", pageNo: 1, pageSize: 10},
		{name: "empty values mean defaults", query: "?pageNo=&pageSize=", pageNo: 1, pageSize: 10},
		{name: "explicit in-bounds page", query: "?pageNo=2&pageSize=5", pageNo: 2, pageSize: 5},
		{name: "upper bounds accepted", query: "?pageNo=1&pageSize=50", pageNo: 1, pageSize: 50},
		{name: "pageNo zero refused", query: "?pageNo=0", wantErr: true},
		{name: "pageNo negative refused", query: "?pageNo=-3", wantErr: true},
		{name: "pageNo garbage refused", query: "?pageNo=two", wantErr: true},
		{name: "pageSize zero refused", query: "?pageSize=0", wantErr: true},
		{name: "pageSize above cap refused", query: "?pageSize=51", wantErr: true},
		{name: "pageSize garbage refused", query: "?pageSize=1.5", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/announcements"+tc.query, nil)
			pageNo, pageSize, err := contentPageParams(req)
			if tc.wantErr {
				if err == nil || !errors.Is(err, errBadContentPage) {
					t.Fatalf("err = %v, want errBadContentPage", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if pageNo != tc.pageNo || pageSize != tc.pageSize {
				t.Fatalf("got (%d, %d), want (%d, %d)", pageNo, pageSize, tc.pageNo, tc.pageSize)
			}
		})
	}
}

// contentText is the one guard every editable CMS field goes through:
// trim, refuse the blank, refuse the oversized.
func TestContentText(t *testing.T) {
	if got, ok := contentText("  <p>Body</p>\n", 100); !ok || got != "<p>Body</p>" {
		t.Fatalf("got (%q, %v), want the trimmed body", got, ok)
	}
	for name, raw := range map[string]string{
		"empty": "",
		"blank": "   \n\t ",
	} {
		if _, ok := contentText(raw, 100); ok {
			t.Fatalf("%s: blank input accepted", name)
		}
	}
	if _, ok := contentText(strings.Repeat("x", 201), maxContentTitleChars); ok {
		t.Fatal("oversized title accepted")
	}
	if _, ok := contentText(strings.Repeat("x", maxContentBodyChars), maxContentBodyChars); !ok {
		t.Fatal("exact-cap body refused")
	}
	if _, ok := contentText(strings.Repeat("x", maxContentBodyChars+1), maxContentBodyChars); ok {
		t.Fatal("oversized body accepted")
	}
}
