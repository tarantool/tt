package aeon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apex/log"

	"github.com/tarantool/go-prompt"
	"github.com/tarantool/tt/cli/aeon/cmd"
	"github.com/tarantool/tt/cli/aeon/pb"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/formatter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type ResultType struct {
	data  map[string][]any
	count int
}

type Client struct {
	title  string
	conn   *grpc.ClientConn
	client pb.AeonRouterServiceClient
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

func getCertificate(args cmd.Ssl) []tls.Certificate {
	if args.CertFile == "" && args.KeyFile == "" {
		return []tls.Certificate{}
	}
	tls_cert, err := tls.LoadX509KeyPair(args.CertFile, args.KeyFile)
	if err != nil {
		log.Fatalf("Could not load client key pair: %v", err)
	}
	return []tls.Certificate{tls_cert}
}

func getTlsConfig(args cmd.Ssl) *tls.Config {
	if args.CaFile == "" {
		return &tls.Config{
			ClientAuth: tls.NoClientCert,
		}
	}

	ca, err := os.ReadFile(args.CaFile)
	if err != nil {
		log.Fatalf("Failed to read CA file: %v", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(ca) {
		log.Fatal("Failed to append CA data")
	}
	return &tls.Config{
		Certificates: getCertificate(args),
		ClientAuth:   tls.RequireAndVerifyClientCert,
		RootCAs:      certPool,
	}
}

func getDialOpts(ctx cmd.ConnectCtx) grpc.DialOption {
	var creds credentials.TransportCredentials
	if ctx.Transport == cmd.TransportSsl {
		creds = credentials.NewTLS(getTlsConfig(ctx.Ssl))
	} else {
		creds = insecure.NewCredentials()
	}
	return grpc.WithTransportCredentials(creds)
}

// NewAeonHandler create new grpc connection to Aeon server.
func NewAeonHandler(ctx cmd.ConnectCtx) *Client {
	c := Client{title: ctx.Address}
	target := makeAddress(ctx)
	var err error
	c.conn, err = grpc.NewClient(target, getDialOpts(ctx))
	if err != nil {
		log.Fatalf("Fail to dial: %v", err)
	}
	c.client = pb.NewAeonRouterServiceClient(c.conn)

	if c.ping() {
		log.Infof("Aeon responses at %q", target)
	} else {
		log.Fatalf("Can't ping to Aeon at %q", target)
	}
	return &c
}

func (c *Client) ping() bool {
	log.Infof("Start ping aeon server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.client.Ping(ctx, &pb.PingRequest{})
	return err == nil
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
		return fmt.Errorf("something wrong with SQL request: %s", resp.Error)
	}
	res := ResultType{
		data:  make(map[string][]any, len(resp.TupleFormat.Names)),
		count: len(resp.Tuples),
	}
	// result := make(ResultType, len(resp.TupleFormat.Names))
	rows := len(resp.Tuples)
	for _, f := range resp.TupleFormat.Names {
		res.data[f] = make([]any, 0, rows)
	}

	for r, row := range resp.Tuples {
		for i, v := range row.Fields {
			k := resp.TupleFormat.Names[i]
			val, err := decodeValue(v)
			if err != nil {
				return fmt.Errorf("tuple %d can't decode %s: %w", r, v.String(), err)
			}
			res.data[k] = append(res.data[k], val)
		}
	}
	return res
}

// asYaml prepare results for formatter.MakeOutput.
func (r ResultType) asYaml() string {
	yaml := "---\n"
	for i := range r.count {
		mark := "-"
		for k, v := range r.data {
			if i < len(v) {
				yaml += fmt.Sprintf("%s %s: %v\n", mark, k, v[i])
				mark = " "
			}
		}
	}
	return yaml
}

// Format produce formatted string according required console.Format settings.
func (r ResultType) Format(f console.Format) string {
	output, err := formatter.MakeOutput(f.Mode, r.asYaml(), f.Opts)
	if err != nil {
		return fmt.Sprintf("can't format output: %s;\nResults:\n%v", err, r)
	}
	return output
}
