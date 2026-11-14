package dial

// Opts are the parameters of a dialer [New] builds.
type Opts struct {
	// Address is where to connect, in a form go-tarantool parses:
	// "host:port", "tcp://host:port", "unix://path", "unix/:path", or a
	// path that starts with "/" or ".".
	Address string
	// Auth is the authentication method. [AuthAuto] takes the server's
	// auth_type on the SSL transport and chap-sha1 on the plain one, where
	// the method of a password sent as it is, [AuthPapSha256], is an error:
	// [ErrPapSha256Unencrypted]. [ParseAuth] reads it from a name.
	Auth Auth
	// User is the user to authenticate as; empty connects as guest.
	User string
	// Password is the password of User.
	Password string
	// SslKeyFile is the path to the client's private SSL key.
	SslKeyFile string
	// SslCertFile is the path to the client's SSL certificate.
	SslCertFile string
	// SslCaFile is the path to the trusted certificate authorities (CA).
	// Without it the server's certificate chain is not verified, only its
	// host name.
	SslCaFile string
	// SslCiphers is a colon-separated list of the SSL ciphers allowed.
	SslCiphers string
	// SslPassword is the password that decrypts the private SSL key. It is
	// tried before SslPasswordFile.
	SslPassword string
	// SslPasswordFile is the path to a file of passwords for the private
	// SSL key, each line tried as one.
	SslPasswordFile string
	// Transport is "plain", "ssl", or "" for SSL when any of the Ssl*
	// fields is set and plain otherwise. Any other value is an error.
	Transport string
}
