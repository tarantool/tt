package dial

// Opts represents set of dialer parameters.
type Opts struct {
	Address         string
	User            string
	Password        string
	SslKeyFile      string
	SslCertFile     string
	SslCaFile       string
	SslCiphers      string
	SslPassword     string
	SslPasswordFile string
	Transport       string
}
