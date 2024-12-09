package cmd

type Ssl struct {
	// KeyFile path to the private SSL key file (optional)
	KeyFile string
	// CertFile path to the SSL certificate file (optional).
	CertFile string
	// CaFile path to the trusted certificate authorities (CA) file (optional)
	CaFile string
}

type ConnectCtx struct {
	Ssl           Ssl
	TransportMode AeonTransport
	Username      string
	Password      string
}
