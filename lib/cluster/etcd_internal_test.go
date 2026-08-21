package cluster

import "testing"

func TestHasSecureEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		endpoints []string
		expected  bool
	}{
		{"empty", nil, false},
		{"plain http", []string{"http://localhost:2379"}, false},
		{"plain no scheme", []string{"localhost:2379"}, false},
		{"single https", []string{"https://localhost:2379"}, true},
		{"mixed", []string{"http://localhost:2379", "https://localhost:2380"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSecureEndpoint(tc.endpoints); got != tc.expected {
				t.Errorf("hasSecureEndpoint(%v) = %v, want %v", tc.endpoints, got, tc.expected)
			}
		})
	}
}

func TestShouldSkipEtcdHostVerify(t *testing.T) {
	cases := []struct {
		name     string
		opts     EtcdOpts
		expected bool
	}{
		{
			name: "plain HTTP without CA",
			opts: EtcdOpts{
				Endpoints: []string{"http://localhost:2379"},
			},
			expected: false,
		},
		{
			name: "explicit skip",
			opts: EtcdOpts{
				Endpoints:      []string{"http://localhost:2379"},
				SkipHostVerify: true,
			},
			expected: true,
		},
		{
			name: "HTTPS without CA",
			opts: EtcdOpts{
				Endpoints: []string{"https://localhost:2379"},
			},
			expected: true,
		},
		{
			name: "mixed endpoints without CA",
			opts: EtcdOpts{
				Endpoints: []string{
					"http://localhost:2379",
					"https://localhost:2380",
				},
			},
			expected: true,
		},
		{
			name: "HTTPS with CA file",
			opts: EtcdOpts{
				Endpoints: []string{"https://localhost:2379"},
				CaFile:    "ca.pem",
			},
			expected: false,
		},
		{
			name: "HTTPS with CA path",
			opts: EtcdOpts{
				Endpoints: []string{"https://localhost:2379"},
				CaPath:    "certs",
			},
			expected: false,
		},
		{
			name: "HTTPS with CA and explicit skip",
			opts: EtcdOpts{
				Endpoints:      []string{"https://localhost:2379"},
				CaFile:         "ca.pem",
				SkipHostVerify: true,
			},
			expected: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSkipEtcdHostVerify(tc.opts); got != tc.expected {
				t.Errorf("shouldSkipEtcdHostVerify() = %v, want %v", got, tc.expected)
			}
		})
	}
}
