package main

import (
	"reflect"
	"testing"
)

func TestAnnounceTXT(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want []string
	}{
		{
			name: "no public address",
			url:  "",
			want: []string{"software=summareader-sync-server", "scheme=https"},
		},
		{
			name: "public address after the scheme",
			url:  "https://sync.example",
			want: []string{"software=summareader-sync-server", "scheme=https", "url=https://sync.example"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := announceTXT(c.url); !reflect.DeepEqual(got, c.want) {
				t.Errorf("announceTXT(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}
