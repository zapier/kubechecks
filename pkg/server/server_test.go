package server

import (
	"testing"

	"github.com/zapier/kubechecks/pkg"
)

func TestHooksPrefix(t *testing.T) {
	tests := []struct {
		name string
		want string
		cfg  *pkg.ServerConfig
	}{
		{
			name: "no-prefix",
			want: "/hooks",
			cfg: &pkg.ServerConfig{
				UrlPrefix: "",
			},
		},
		{
			name: "prefix-no-slash",
			want: "/test/hooks",
			cfg: &pkg.ServerConfig{
				UrlPrefix: "test",
			},
		},
		{
			name: "prefix-trailing-slash",
			want: "/test/hooks",
			cfg: &pkg.ServerConfig{
				UrlPrefix: "test/",
			},
		},
		{
			name: "prefix-leading-slash",
			want: "/test/hooks",
			cfg: &pkg.ServerConfig{
				UrlPrefix: "/test",
			},
		},
		{
			name: "prefix-slash-sandwich",
			want: "/test/hooks",
			cfg: &pkg.ServerConfig{
				UrlPrefix: "/test/",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(tt.cfg)
			if got := s.hooksPrefix(); got != tt.want {
				t.Errorf("hooksPrefix() = %v, want %v", got, tt.want)
			}
		})
	}
}
