package server

import "testing"

func TestHooksPrefix(t *testing.T) {
	tests := []struct {
		name string
		want string
		cfg  *ServerConfig
	}{
		{
			name: "no-prefix",
			want: "/hooks",
			cfg: &ServerConfig{
				UrlPrefix: "",
			},
		},
		{
			name: "prefix-no-slash",
			want: "/test/hooks",
			cfg: &ServerConfig{
				UrlPrefix: "test",
			},
		},
		{
			name: "prefix-trailing-slash",
			want: "/test/hooks",
			cfg: &ServerConfig{
				UrlPrefix: "test/",
			},
		},
		{
			name: "prefix-leading-slash",
			want: "/test/hooks",
			cfg: &ServerConfig{
				UrlPrefix: "/test",
			},
		},
		{
			name: "prefix-slash-sandwich",
			want: "/test/hooks",
			cfg: &ServerConfig{
				UrlPrefix: "/test/",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(tt.cfg)
			if got := s.HooksPrefix(); got != tt.want {
				t.Errorf("HooksPrefix() = %v, want %v", got, tt.want)
			}
		})
	}
}
