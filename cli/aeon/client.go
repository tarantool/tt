package aeon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/apex/log"

	"github.com/tarantool/go-prompt"
	"github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/aeon/pb"
	"github.com/tarantool/tt/cli/connector"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client structure with parameters for gRPC connection to Aeon.
type Client struct {
	title  string
	conn   *grpc.ClientConn
	client pb.SQLServiceClient
}

func makeAddress(ctx cmd.ConnectCtx) string {
	if ctx.Network == connector.UnixNetwork {
		if strings.HasPrefix(ctx.Address, "@") {
			return "unix-abstract:" + (ctx.Address)[1:]
		}
		return "unix:" + ctx.Address
	}
	return ctx.Address
}

func getCertificate(args cmd.Ssl) (tls.Certificate, error) {
	if args.CertFile == "" && args.KeyFile == "" {
		return tls.Certificate{}, nil
	}
	tls_cert, err := tls.LoadX509KeyPair(args.CertFile, args.KeyFile)
	if err != nil {
		return tls_cert, fmt.Errorf("could not load client key pair: %w", err)
	}
	return tls_cert, nil
}

func getTlsConfig(args cmd.Ssl) (*tls.Config, error) {
	var pool *x509.CertPool

	if args.CaFile != "" {
		ca, err := os.ReadFile(args.CaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("failed to append CA data")
		}
	}
	// Else if RootCAs is nil, TLS uses the host's root CA set.

	cert, err := getCertificate(args)
	if err != nil {
		return nil, fmt.Errorf("failed get certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
	}, nil
}

func getDialOpts(ctx cmd.ConnectCtx) (grpc.DialOption, error) {
	var creds credentials.TransportCredentials
	if ctx.Transport == cmd.TransportSsl {
		config, err := getTlsConfig(ctx.Ssl)
		if err != nil {
			return nil, fmt.Errorf("not tls config: %w", err)
		}
		creds = credentials.NewTLS(config)
	} else {
		creds = insecure.NewCredentials()
	}
	return grpc.WithTransportCredentials(creds), nil
}

// NewAeonHandler create new grpc connection to Aeon server.
func NewAeonHandler(ctx cmd.ConnectCtx) (*Client, error) {
	c := Client{title: ctx.Address}
	target := makeAddress(ctx)
	// var err error
	opt, err := getDialOpts(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	c.conn, err = grpc.NewClient(target, opt)
	if err != nil {
		return nil, fmt.Errorf("fail to dial: %w", err)
	}
	if err := c.ping(); err == nil {
		log.Infof("Aeon responses at %q", target)
	} else {
		return nil, fmt.Errorf("can't ping to Aeon at %q: %w", target, err)
	}

	c.client = pb.NewSQLServiceClient(c.conn)
	return &c, nil
}

func (c *Client) ping() error {
	log.Infof("Start ping aeon server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	diag := pb.NewDiagServiceClient(c.conn)
	_, err := diag.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		log.Warnf("Aeon ping %s", err)
	}
	return err
}

// Title implements console.Handler interface.
func (c *Client) Title() string {
	return c.title
}

// Validate implements console.Handler interface.
func (c *Client) Validate(input string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	check, err := c.client.SQLCheck(ctx, &pb.SQLRequest{Query: input})
	if err != nil {
		log.Warnf("Aeon validate %s\nFor request: %q", err, input)
		return false
	}

	return check.Status == pb.SQLCheckStatus_SQL_QUERY_VALID
}

// Execute implements console.Handler interface.
func (c *Client) Execute(input string) any {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.client.SQL(ctx, &pb.SQLRequest{Query: input})
	if err != nil {
		return err
	}
	return parseSQLResponse(resp)
}

// Stop implements console.Handler interface.
func (c *Client) Close() {
	c.conn.Close()
}

// Complete implements console.Handler interface.
func (c *Client) Complete(input prompt.Document) []prompt.Suggest {
	// TODO: waiting until there is support from Aeon side.
	return nil
}

// parseSQLResponse returns result as table in map.
// Where keys is name of columns. And body is array of values.
// On any issue return an error.
func parseSQLResponse(resp *pb.SQLResponse) any {
	if resp.Error != nil {
		return resultError{resp.Error}
	}
	if resp.TupleFormat == nil {
		return resultType{}
	}
	res := resultType{
		names: slices.Clone(resp.TupleFormat.Names),
		rows:  make([]resultRow, len(resp.Tuples)),
	}
	for i := range resp.Tuples {
		res.rows[i] = make([]any, 0, len(resp.TupleFormat.Names))
	}

	for r, row := range resp.Tuples {
		for _, v := range row.Fields {
			val, err := decodeValue(v)
			if err != nil {
				return fmt.Errorf("tuple %d can't decode %s: %w", r, v.String(), err)
			}
			res.rows[r] = append(res.rows[r], val)
		}
	}
	return res
}
