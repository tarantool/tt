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
	// Network is kind of transport layer.
	Network string
	// Address is a connection URL, unix socket address and etc.
	Address string
}

// Advertise structure groups connection parameters.
type Advertise struct {
	// Uri is a connection URL, unix socket address and etc.
	Uri string `mapstructure:"uri"`
	// Group of connection parameters.
	Params AdvertiseParams `mapstructure:"params"`
}

// AdvertiseParams groups connection parameters.
type AdvertiseParams struct {
	// Transport is a connection mode.
	Transport string `mapstructure:"transport"`
	// KeyFile path to the private SSL key file (optional).
	KeyFile string `mapstructure:"ssl_key_file"`
	// CertFile path to the SSL certificate file (optional).
	CertFile string `mapstructure:"ssl_cert_file"`
	// CaFile path to the trusted certificate authorities (CA) file (optional).
	CaFile string `mapstructure:"ssl_ca_file"`
}
