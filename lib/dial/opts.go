package dial

// // Transport is a type, with a restriction on the list of supported connection modes.
// type Transport string

// func (t Transport) String() string {
// 	return string(t)
// }

// const (
// 	TransportPlain Transport = "plain"
// 	TransportSsl   Transport = "ssl"
// )

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
	// Transport       Transport
}

// // dial creates dialer according to the opts.Transport
// func Dial(opts Opts) (tarantool.Dialer, error) {
// 	switch opts.Transport {
// 	case TransportPlain:
// 		return usual_dialer(opts)
// 	case TransportSsl:
// 		return ssl_dialer(opts)
// 	default:
// 		return nil, fmt.Errorf("unsupported transport type: %s", opts.Transport)
// 	}
// }
