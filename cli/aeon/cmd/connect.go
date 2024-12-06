package cmd

// Ssl structure groups paths to ssl key files.
type Ssl struct {
	// KeyFile path to the private SSL key file (optional).
	KeyFile string
	// CertFile path to the SSL certificate file (optional).
	CertFile string
	// CaFile path to the trusted certificate authorities (CA) file (optional).
	CaFile string
}

// ConnectCtx keeps context information for aeon connection.
type ConnectCtx struct {
	// Ssl group of paths to ssl key files.
	Ssl Ssl
	// Transport is a connection mode.
	Transport Transport
}
