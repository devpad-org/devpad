package workspace

import (
	"net/http/httptest"
	"testing"
)

func TestCheckWebSocketOrigin(t *testing.T) {
	tests := []struct {
		name           string
		origin         string
		host           string
		forwardedHost  string
		allowedOrigins []string
		want           bool
	}{
		{
			name: "missing origin",
			host: "devpad.example.com",
			want: true,
		},
		{
			name:           "configured origin",
			origin:         "https://devpad.example.com",
			host:           "internal:8080",
			allowedOrigins: []string{"https://devpad.example.com"},
			want:           true,
		},
		{
			name:   "same request host",
			origin: "https://devpad.example.com",
			host:   "devpad.example.com",
			want:   true,
		},
		{
			name:   "same named server with port",
			origin: "http://midnights:8080",
			host:   "midnights:8080",
			want:   true,
		},
		{
			name:          "untrusted forwarded host is ignored",
			origin:        "https://devpad.example.com",
			host:          "internal:8080",
			forwardedHost: "devpad.example.com",
			want:          false,
		},
		{
			name:   "localhost dev server different port",
			origin: "http://localhost:5173",
			host:   "localhost:8080",
			want:   true,
		},
		{
			name:   "different host",
			origin: "https://evil.example.com",
			host:   "devpad.example.com",
			want:   false,
		},
		{
			name:   "different non-loopback port",
			origin: "https://devpad.example.com:5173",
			host:   "devpad.example.com:8080",
			want:   false,
		},
		{
			name:   "invalid origin",
			origin: "://bad",
			host:   "devpad.example.com",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(nil, tt.allowedOrigins)
			req := httptest.NewRequest("GET", "/api/workspaces/1/watch", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwardedHost)
			}

			if got := handler.checkWebSocketOrigin(req); got != tt.want {
				t.Fatalf("checkWebSocketOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}
