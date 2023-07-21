package server

import (
	"github.com/zapier/kubechecks/pkg/config"
	"testing"
)

func TestHooksPrefix(t *testing.T) {
	tests := []struct {
		name string
		want string
		cfg  *config.ServerConfig
	}{
		{
			name: "no-prefix",
			want: "/hooks",
			cfg: &config.ServerConfig{
				UrlPrefix: "",
			},
		},
		{
			name: "prefix-no-slash",
			want: "/test/hooks",
			cfg: &config.ServerConfig{
				UrlPrefix: "test",
			},
		},
		{
			name: "prefix-trailing-slash",
			want: "/test/hooks",
			cfg: &config.ServerConfig{
				UrlPrefix: "test/",
			},
		},
		{
			name: "prefix-leading-slash",
			want: "/test/hooks",
			cfg: &config.ServerConfig{
				UrlPrefix: "/test",
			},
		},
		{
			name: "prefix-slash-sandwich",
			want: "/test/hooks",
			cfg: &config.ServerConfig{
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
